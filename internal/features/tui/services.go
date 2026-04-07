package tui

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"

	"springfield/internal/core/agents"
	"springfield/internal/core/agents/claude"
	"springfield/internal/core/agents/codex"
	"springfield/internal/core/agents/gemini"
	"springfield/internal/core/config"
	coreexec "springfield/internal/core/exec"
	"springfield/internal/core/runtime"
	"springfield/internal/features/conductor"
	"springfield/internal/features/doctor"
	"springfield/internal/features/ralph"
	"springfield/internal/storage"
)

type runtimeServices struct {
	cwd      func() (string, error)
	lookPath func(string) (string, error)
}

func newRuntimeServices(cwd func() (string, error), lookPath func(string) (string, error)) Services {
	if cwd == nil {
		cwd = os.Getwd
	}
	if lookPath == nil {
		lookPath = exec.LookPath
	}

	return runtimeServices{
		cwd:      cwd,
		lookPath: lookPath,
	}
}

func (s runtimeServices) SetupStatus() SetupStatus {
	workingDir, err := s.cwd()
	if err != nil {
		return SetupStatus{Error: err.Error()}
	}

	status := SetupStatus{
		WorkingDir:  workingDir,
		ProjectRoot: workingDir,
		ConfigPath:  filepath.Join(workingDir, config.FileName),
	}

	loaded, loadErr := config.LoadFrom(workingDir)
	var missingConfig *config.MissingConfigError
	switch {
	case loadErr == nil:
		status.ProjectRoot = loaded.RootDir
		status.ConfigPath = loaded.Path
		status.ConfigPresent = true
	case errors.As(loadErr, &missingConfig):
	default:
		status.Error = loadErr.Error()
		return status
	}

	status.RuntimeDir = filepath.Join(status.ProjectRoot, storage.DirName)
	if info, err := os.Stat(status.RuntimeDir); err == nil && info.IsDir() {
		status.RuntimePresent = true
	}

	status.ConductorConfigPath = filepath.Join(status.RuntimeDir, "conductor", "config.json")
	if info, err := os.Stat(status.ConductorConfigPath); err == nil && !info.IsDir() {
		status.ConductorConfigReady = true
	}

	return status
}

func (s runtimeServices) InitProject() (config.InitResult, error) {
	status := s.SetupStatus()
	targetDir := status.ProjectRoot
	if targetDir == "" {
		targetDir = status.WorkingDir
	}

	return config.Init(targetDir)
}

func (s runtimeServices) RalphSummary() RalphSummary {
	status := s.SetupStatus()
	if status.Error != "" {
		return RalphSummary{Reason: status.Error}
	}
	if !status.ConfigPresent {
		return RalphSummary{Reason: "Run Guided Setup first to create springfield.toml."}
	}

	workspace, err := ralph.OpenRoot(status.ProjectRoot)
	if err != nil {
		return RalphSummary{Reason: err.Error()}
	}

	plans, err := workspace.ListPlans()
	if err != nil {
		return RalphSummary{Reason: err.Error()}
	}

	runs, err := workspace.ListRuns()
	if err != nil {
		return RalphSummary{Reason: err.Error()}
	}

	summary := RalphSummary{
		Ready:      true,
		Plans:      make([]RalphPlanSummary, 0, len(plans)),
		RecentRuns: make([]RalphRunSummary, 0, minInt(len(runs), 5)),
	}

	for _, plan := range plans {
		nextID := "-"
		nextTitle := "all stories passed"
		if story, ok := plan.NextEligible(); ok {
			nextID = story.ID
			nextTitle = story.Title
		}

		summary.Plans = append(summary.Plans, RalphPlanSummary{
			Name:           plan.Name,
			Project:        plan.Spec.Project,
			StoryCount:     len(plan.Spec.Stories),
			NextStoryID:    nextID,
			NextStoryTitle: nextTitle,
		})
	}

	for index := len(runs) - 1; index >= 0 && len(summary.RecentRuns) < 5; index-- {
		run := runs[index]
		summary.RecentRuns = append(summary.RecentRuns, RalphRunSummary{
			PlanName: run.PlanName,
			StoryID:  run.StoryID,
			Status:   run.Status,
		})
	}

	return summary
}

func (s runtimeServices) SetupConductor(input ConductorSetupInput) (ConductorSetupResult, error) {
	status := s.SetupStatus()
	if status.Error != "" {
		return ConductorSetupResult{}, errors.New(status.Error)
	}
	if !status.ConfigPresent || !status.RuntimePresent {
		return ConductorSetupResult{}, errors.New("run Guided Setup first to initialize the project")
	}

	loaded, err := config.LoadFrom(status.ProjectRoot)
	if err != nil {
		return ConductorSetupResult{}, err
	}

	opts := conductor.SetupDefaults()
	opts.Tool = loaded.Config.Project.DefaultAgent
	opts.PlansDir = input.PlansDir
	opts.WorktreeBase = input.WorktreeBase
	opts.MaxRetries = input.MaxRetries
	opts.RalphIterations = input.RalphIterations
	opts.RalphTimeout = input.RalphTimeout
	opts.UpdateGitignore = input.UpdateGitignore

	result, err := conductor.Setup(status.ProjectRoot, opts)
	if err != nil {
		return ConductorSetupResult{}, err
	}

	return ConductorSetupResult{
		Created: result.Created,
		Reused:  result.Reused,
		Path:    result.Path,
	}, nil
}

func (s runtimeServices) ConductorSummary() ConductorSummary {
	status := s.SetupStatus()
	if status.Error != "" {
		return ConductorSummary{Reason: status.Error}
	}
	if !status.ConfigPresent {
		return ConductorSummary{Reason: "Run Guided Setup first to create springfield.toml."}
	}
	if !status.ConductorConfigReady {
		return ConductorSummary{Reason: "Conductor config not found. Run Guided Setup or `springfield conductor setup` to generate it."}
	}

	project, err := conductor.LoadProject(status.ProjectRoot)
	if err != nil {
		return ConductorSummary{Reason: err.Error()}
	}

	diagnosis := conductor.Diagnose(project)
	failures := make([]ConductorPlanFailure, 0, len(diagnosis.Failures))
	for _, f := range diagnosis.Failures {
		failures = append(failures, ConductorPlanFailure{
			Plan:         f.Plan,
			Error:        f.Error,
			Agent:        f.Agent,
			EvidencePath: f.EvidencePath,
			Attempts:     f.Attempts,
		})
	}

	return ConductorSummary{
		Ready:     true,
		Completed: diagnosis.Completed,
		Total:     diagnosis.Total,
		Done:      diagnosis.Done,
		Failures:  failures,
		NextStep:  diagnosis.NextStep,
	}
}

func (s runtimeServices) RunRalphNext(planName string, onEvent func(RuntimeEvent)) (RalphRunResult, error) {
	status := s.SetupStatus()
	if status.Error != "" {
		return RalphRunResult{}, errors.New(status.Error)
	}
	if !status.ConfigPresent {
		return RalphRunResult{}, errors.New("run Guided Setup first")
	}

	loaded, err := config.LoadFrom(status.ProjectRoot)
	if err != nil {
		return RalphRunResult{}, err
	}

	workspace, err := ralph.OpenRoot(status.ProjectRoot)
	if err != nil {
		return RalphRunResult{}, err
	}

	registry := agents.NewRegistry(
		claude.New(s.lookPath),
		codex.New(s.lookPath),
		gemini.New(s.lookPath),
	)
	runner := runtime.NewRunner(registry)
	executor := ralph.NewRuntimeExecutor(runner, agents.ID(loaded.Config.Project.DefaultAgent), status.ProjectRoot)
	if onEvent != nil {
		executor.OnEvent = func(e coreexec.Event) {
			onEvent(RuntimeEvent{Source: string(e.Type), Data: e.Data})
		}
	}

	record, err := workspace.RunNext(planName, executor)
	if err != nil {
		return RalphRunResult{}, err
	}

	return RalphRunResult{
		PlanName: record.PlanName,
		StoryID:  record.StoryID,
		Status:   record.Status,
		Error:    record.Error,
	}, nil
}

func (s runtimeServices) RunConductorNext(onEvent func(RuntimeEvent)) (ConductorRunResult, error) {
	status := s.SetupStatus()
	if status.Error != "" {
		return ConductorRunResult{}, errors.New(status.Error)
	}
	if !status.ConductorConfigReady {
		return ConductorRunResult{}, errors.New("conductor config not ready")
	}

	loaded, err := config.LoadFrom(status.ProjectRoot)
	if err != nil {
		return ConductorRunResult{}, err
	}

	project, err := conductor.LoadProject(status.ProjectRoot)
	if err != nil {
		return ConductorRunResult{}, err
	}

	registry := agents.NewRegistry(
		claude.New(s.lookPath),
		codex.New(s.lookPath),
		gemini.New(s.lookPath),
	)
	runner := runtime.NewRunner(registry)

	plansDir := project.Config.PlansDir
	if !filepath.IsAbs(plansDir) {
		plansDir = filepath.Join(status.ProjectRoot, plansDir)
	}
	executor := conductor.NewRuntimeExecutor(runner, agents.ID(loaded.Config.Project.DefaultAgent), plansDir, status.ProjectRoot)
	if onEvent != nil {
		executor.OnEvent = func(e coreexec.Event) {
			onEvent(RuntimeEvent{Source: string(e.Type), Data: e.Data})
		}
	}

	conductorRunner := conductor.NewRunner(project, executor)
	ran, done, err := conductorRunner.RunNext()
	if err != nil {
		return ConductorRunResult{Ran: ran, Done: done, Error: err.Error()}, err
	}

	return ConductorRunResult{Ran: ran, Done: done}, nil
}

func (s runtimeServices) DetectAgents() []AgentDetection {
	registry := agents.NewRegistry(
		claude.New(s.lookPath),
		codex.New(s.lookPath),
		gemini.New(s.lookPath),
	)
	detections := registry.DetectAll(context.Background())
	result := make([]AgentDetection, len(detections))
	for i, d := range detections {
		result[i] = AgentDetection{
			ID:        string(d.ID),
			Name:      d.Name,
			Installed: d.Status == agents.DetectionStatusAvailable,
		}
	}
	return result
}

func (s runtimeServices) ConductorCurrentConfig() *ConductorCurrentConfig {
	status := s.SetupStatus()
	if !status.ConductorConfigReady {
		return nil
	}
	project, err := conductor.LoadProject(status.ProjectRoot)
	if err != nil {
		return nil
	}
	return &ConductorCurrentConfig{
		PlansDir:        project.Config.PlansDir,
		WorktreeBase:    project.Config.WorktreeBase,
		MaxRetries:      project.Config.MaxRetries,
		RalphIterations: project.Config.RalphIterations,
		RalphTimeout:    project.Config.RalphTimeout,
	}
}

func (s runtimeServices) DoctorSummary() doctor.Report {
	registry := agents.NewRegistry(
		claude.New(s.lookPath),
		codex.New(s.lookPath),
		gemini.New(s.lookPath),
	)

	return doctor.Run(context.Background(), registry)
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}
