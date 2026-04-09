package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"springfield/internal/core/agents"
	"springfield/internal/core/agents/claude"
	"springfield/internal/core/agents/codex"
	"springfield/internal/core/agents/gemini"
	"springfield/internal/core/config"
	coreexec "springfield/internal/core/exec"
	"springfield/internal/core/runtime"
	"springfield/internal/features/conductor"
	"springfield/internal/features/doctor"
	"springfield/internal/features/planner"
	"springfield/internal/features/ralph"
	"springfield/internal/features/workflow"
	"springfield/internal/storage"
)

type runtimeServices struct {
	cwd                func() (string, error)
	lookPath           func(string) (string, error)
	newPlanningSession func(projectRoot string) planningSession
	planning           *planningState
}

type planningSession interface {
	Next(input string) (planner.Response, error)
}

type planningState struct {
	projectRoot string
	session     planningSession
	request     string
	answers     []string
	draft       planner.Response
	hasDraft    bool
}

func priorityAgentIDs(priority []string) []agents.ID {
	ids := make([]agents.ID, 0, len(priority))
	for _, id := range priority {
		if id == "" {
			continue
		}
		ids = append(ids, agents.ID(id))
	}
	return ids
}

func newRuntimeServices(cwd func() (string, error), lookPath func(string) (string, error)) Services {
	if cwd == nil {
		cwd = os.Getwd
	}
	if lookPath == nil {
		lookPath = exec.LookPath
	}

	return &runtimeServices{
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

func (s runtimeServices) SpringfieldStatus() SpringfieldStatus {
	root, workID, err := s.springfieldTarget()
	if err != nil {
		return SpringfieldStatus{Reason: err.Error()}
	}

	runner, err := workflow.NewRuntimeRunner(root, s.lookPath, nil)
	if err != nil {
		return SpringfieldStatus{Reason: err.Error()}
	}

	status, err := runner.Status(root, workID)
	if err != nil {
		return SpringfieldStatus{Reason: err.Error()}
	}

	workstreams := make([]SpringfieldWorkstreamStatus, 0, len(status.Workstreams))
	for _, workstream := range status.Workstreams {
		workstreams = append(workstreams, SpringfieldWorkstreamStatus{
			Name:         workstream.Name,
			Title:        workstream.Title,
			Status:       workstream.Status,
			Error:        workstream.Error,
			EvidencePath: workstream.EvidencePath,
		})
	}

	return SpringfieldStatus{
		Ready:       true,
		WorkID:      status.WorkID,
		Title:       status.Title,
		Split:       status.Split,
		Status:      status.Status,
		Workstreams: workstreams,
	}
}

func (s runtimeServices) SpringfieldDiagnosis() SpringfieldDiagnosis {
	root, workID, err := s.springfieldTarget()
	if err != nil {
		return SpringfieldDiagnosis{NextStep: err.Error()}
	}

	runner, err := workflow.NewRuntimeRunner(root, s.lookPath, nil)
	if err != nil {
		return SpringfieldDiagnosis{NextStep: err.Error()}
	}

	diagnosis, err := runner.Diagnose(root, workID)
	if err != nil {
		return SpringfieldDiagnosis{NextStep: err.Error()}
	}

	failures := make([]SpringfieldDiagnosisFailure, 0, len(diagnosis.Failures))
	for _, failure := range diagnosis.Failures {
		failures = append(failures, SpringfieldDiagnosisFailure{
			Workstream:   failure.Workstream,
			Title:        failure.Title,
			Error:        failure.Error,
			EvidencePath: failure.EvidencePath,
		})
	}

	return SpringfieldDiagnosis{
		WorkID:   diagnosis.WorkID,
		Status:   diagnosis.Status,
		NextStep: diagnosis.NextStep,
		Failures: failures,
	}
}

func (s runtimeServices) RunSpringfieldWork(onEvent func(RuntimeEvent)) (SpringfieldRunResult, error) {
	return s.runSpringfield(onEvent, false)
}

func (s runtimeServices) ResumeSpringfieldWork(onEvent func(RuntimeEvent)) (SpringfieldRunResult, error) {
	return s.runSpringfield(onEvent, true)
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
	priority := loaded.Config.EffectivePriority()
	opts.Tool = priority[0]
	if len(priority) > 1 {
		opts.FallbackTool = priority[1]
	}
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
		Created:          result.Created,
		Reused:           result.Reused,
		Path:             result.Path,
		GitignoreUpdated: result.GitignoreUpdated,
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
	priority := loaded.Config.EffectivePriority()
	executor := ralph.NewRuntimeExecutor(runner, priorityAgentIDs(priority), status.ProjectRoot, loaded.Config.ExecutionSettings())
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
	priority := loaded.Config.EffectivePriority()
	executor := conductor.NewRuntimeExecutor(runner, priorityAgentIDs(priority), plansDir, status.ProjectRoot, loaded.Config.ExecutionSettings())
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

func (s runtimeServices) AgentPriority() []string {
	status := s.SetupStatus()
	if !status.ConfigPresent {
		return nil
	}
	loaded, err := config.LoadFrom(status.ProjectRoot)
	if err != nil {
		return nil
	}
	return loaded.Config.EffectivePriority()
}

func (s runtimeServices) AgentExecutionModes() AgentExecutionModes {
	status := s.SetupStatus()
	if !status.ConfigPresent {
		return AgentExecutionModes{}
	}
	loaded, err := config.LoadFrom(status.ProjectRoot)
	if err != nil {
		return AgentExecutionModes{}
	}
	modes := loaded.Config.ExecutionModes()
	return AgentExecutionModes{
		Claude: string(modes.Claude),
		Codex:  string(modes.Codex),
	}
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

func (s runtimeServices) SaveAgentPriority(priority []string) error {
	status := s.SetupStatus()
	if status.Error != "" {
		return errors.New(status.Error)
	}
	loaded, err := config.LoadFrom(status.ProjectRoot)
	if err != nil {
		return err
	}
	loaded.Config.Project.AgentPriority = priority
	return config.Save(loaded)
}

func (s runtimeServices) SaveAgentExecutionModes(input SaveAgentExecutionModesInput) error {
	status := s.SetupStatus()
	if status.Error != "" {
		return errors.New(status.Error)
	}
	loaded, err := config.LoadFrom(status.ProjectRoot)
	if err != nil {
		return err
	}

	loaded.Config.ApplyExecutionMode(string(agents.AgentClaude), config.ExecutionMode(input.Claude))
	loaded.Config.ApplyExecutionMode(string(agents.AgentCodex), config.ExecutionMode(input.Codex))

	return config.Save(loaded)
}

func (s runtimeServices) EnsureRecommendedExecutionDefaults() error {
	status := s.SetupStatus()
	if status.Error != "" {
		return errors.New(status.Error)
	}
	loaded, err := config.LoadFrom(status.ProjectRoot)
	if err != nil {
		return err
	}
	if loaded.Config.HasAnyExecutionSettings() {
		return nil
	}

	loaded.Config.ApplyRecommendedExecutionDefaults()
	return config.Save(loaded)
}

func (s runtimeServices) UpdateConductor(input ConductorSetupInput) (ConductorSetupResult, error) {
	status := s.SetupStatus()
	if status.Error != "" {
		return ConductorSetupResult{}, errors.New(status.Error)
	}
	loaded, err := config.LoadFrom(status.ProjectRoot)
	if err != nil {
		return ConductorSetupResult{}, err
	}
	opts := conductor.SetupDefaults()
	priority := loaded.Config.EffectivePriority()
	opts.Tool = priority[0]
	if len(priority) > 1 {
		opts.FallbackTool = priority[1]
	}
	opts.PlansDir = input.PlansDir
	opts.WorktreeBase = input.WorktreeBase
	opts.MaxRetries = input.MaxRetries
	opts.RalphIterations = input.RalphIterations
	opts.RalphTimeout = input.RalphTimeout
	opts.UpdateGitignore = input.UpdateGitignore

	result, err := conductor.UpdateConfig(status.ProjectRoot, opts)
	if err != nil {
		return ConductorSetupResult{}, err
	}
	return ConductorSetupResult{
		Created:          false,
		Reused:           false,
		Path:             result.Path,
		GitignoreUpdated: result.GitignoreUpdated,
	}, nil
}

func (s runtimeServices) runSpringfield(onEvent func(RuntimeEvent), resume bool) (SpringfieldRunResult, error) {
	root, workID, err := s.springfieldTarget()
	if err != nil {
		return SpringfieldRunResult{}, err
	}

	handler := coreexec.EventHandler(nil)
	if onEvent != nil {
		handler = func(e coreexec.Event) {
			onEvent(RuntimeEvent{Source: string(e.Type), Data: e.Data})
		}
	}

	runner, err := workflow.NewRuntimeRunner(root, s.lookPath, handler)
	if err != nil {
		return SpringfieldRunResult{}, err
	}

	var result workflow.RunResult
	if resume {
		result, err = runner.Resume(root, workID)
	} else {
		result, err = runner.Run(root, workID)
	}
	if err != nil {
		return SpringfieldRunResult{
			WorkID: result.WorkID,
			Status: result.Status,
			Error:  result.Error,
		}, err
	}

	return SpringfieldRunResult{
		WorkID: result.WorkID,
		Status: result.Status,
		Error:  result.Error,
	}, nil
}

func (s runtimeServices) springfieldTarget() (string, string, error) {
	status := s.SetupStatus()
	if status.Error != "" {
		return "", "", errors.New(status.Error)
	}
	if !status.ConfigPresent {
		return "", "", errors.New("run Guided Setup first to create springfield.toml")
	}

	workID, err := workflow.CurrentWorkID(status.ProjectRoot)
	if err != nil {
		return "", "", err
	}

	return status.ProjectRoot, workID, nil
}

func (s runtimeServices) DoctorSummary() doctor.Report {
	registry := agents.NewRegistry(
		claude.New(s.lookPath),
		codex.New(s.lookPath),
		gemini.New(s.lookPath),
	)

	return doctor.Run(context.Background(), registry)
}

func (s *runtimeServices) PlanWork(input string) (PlanWorkResult, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return PlanWorkResult{}, errors.New("enter a work request first")
	}

	if s.planning == nil || s.planning.hasDraft {
		projectRoot, err := s.planningProjectRoot()
		if err != nil {
			return PlanWorkResult{}, err
		}
		s.planning = &planningState{
			projectRoot: projectRoot,
			session:     s.planningSession(projectRoot),
			request:     trimmed,
		}
	} else {
		s.planning.answers = append(s.planning.answers, trimmed)
	}

	resp, err := s.planning.session.Next(trimmed)
	if err != nil {
		return PlanWorkResult{}, err
	}
	return s.updatePlanningResult(resp), nil
}

func (s *runtimeServices) RegeneratePlannedWork() (PlanWorkResult, error) {
	if s.planning == nil || strings.TrimSpace(s.planning.request) == "" {
		return PlanWorkResult{}, errors.New("no planned work to regenerate")
	}

	request := s.planning.request
	answers := append([]string(nil), s.planning.answers...)
	projectRoot := s.planning.projectRoot

	state := &planningState{
		projectRoot: projectRoot,
		session:     s.planningSession(projectRoot),
		request:     request,
		answers:     answers,
	}

	resp, err := state.session.Next(request)
	if err != nil {
		return PlanWorkResult{}, err
	}
	for _, answer := range answers {
		if resp.Mode != planner.ModeQuestion {
			return PlanWorkResult{}, errors.New("planner regenerate replay did not return expected follow-up question")
		}
		resp, err = state.session.Next(answer)
		if err != nil {
			return PlanWorkResult{}, err
		}
	}

	s.planning = state
	return s.updatePlanningResult(resp), nil
}

func (s *runtimeServices) ApprovePlannedWork() error {
	if s.planning == nil || !s.planning.hasDraft {
		return errors.New("no planned work draft ready to approve")
	}

	return workflow.WriteDraft(s.planning.projectRoot, workflow.Draft{
		RequestBody: s.planning.request,
		Response:    s.planning.draft,
	})
}

func (s *runtimeServices) ResetPlannedWork() {
	s.planning = nil
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func (s *runtimeServices) planningProjectRoot() (string, error) {
	status := s.SetupStatus()
	if status.Error != "" {
		return "", errors.New(status.Error)
	}
	if status.ProjectRoot != "" {
		return status.ProjectRoot, nil
	}
	if status.WorkingDir != "" {
		return status.WorkingDir, nil
	}
	return "", errors.New("could not resolve project root for planning")
}

func (s *runtimeServices) planningSession(projectRoot string) planningSession {
	if s.newPlanningSession != nil {
		return s.newPlanningSession(projectRoot)
	}
	return &planner.Session{
		ProjectRoot: projectRoot,
		Runner: plannerRuntimeRunner{
			projectRoot: projectRoot,
			lookPath:    s.lookPath,
		},
	}
}

func (s *runtimeServices) updatePlanningResult(resp planner.Response) PlanWorkResult {
	if s.planning == nil {
		return summarizePlan(resp)
	}
	if resp.Mode == planner.ModeDraft {
		s.planning.draft = resp
		s.planning.hasDraft = true
	} else {
		s.planning.draft = planner.Response{}
		s.planning.hasDraft = false
	}
	return summarizePlan(resp)
}

func summarizePlan(resp planner.Response) PlanWorkResult {
	if resp.Mode == planner.ModeQuestion {
		return PlanWorkResult{Question: resp.Question}
	}

	workstreams := make([]PlannedWorkstreamSummary, 0, len(resp.Workstreams))
	for _, workstream := range resp.Workstreams {
		workstreams = append(workstreams, PlannedWorkstreamSummary{
			Name:    workstream.Name,
			Title:   workstream.Title,
			Summary: workstream.Summary,
		})
	}

	return PlanWorkResult{
		Draft: &PlannedWorkDraft{
			WorkID:      resp.WorkID,
			Title:       resp.Title,
			Summary:     resp.Summary,
			Split:       resp.Split,
			Workstreams: workstreams,
		},
	}
}

type plannerRuntimeRunner struct {
	projectRoot string
	lookPath    func(string) (string, error)
}

func (r plannerRuntimeRunner) Run(prompt string) (string, error) {
	registry := agents.NewRegistry(
		claude.New(r.lookPath),
		codex.New(r.lookPath),
		gemini.New(r.lookPath),
	)
	priority, settings, err := r.loadConfig()
	if err != nil {
		return "", err
	}

	result := runtime.NewRunner(registry).Run(context.Background(), runtime.Request{
		AgentIDs:          priority,
		Prompt:            prompt,
		WorkDir:           r.projectRoot,
		ExecutionSettings: settings,
	})
	if result.Err != nil {
		return "", result.Err
	}
	if result.Status != runtime.StatusPassed {
		return "", fmt.Errorf("planner agent %q failed", result.Agent)
	}

	lines := make([]string, 0, len(result.Events))
	for _, event := range result.Events {
		if event.Type != coreexec.EventStdout {
			continue
		}
		lines = append(lines, event.Data)
	}

	output := strings.TrimSpace(strings.Join(lines, "\n"))
	if output == "" {
		return "", fmt.Errorf("planner agent %q returned no stdout", result.Agent)
	}
	return output, nil
}

func (r plannerRuntimeRunner) loadConfig() ([]agents.ID, agents.ExecutionSettings, error) {
	loaded, err := config.LoadFrom(r.projectRoot)
	if err == nil {
		return priorityAgentIDs(loaded.Config.EffectivePriority()), loaded.Config.ExecutionSettings(), nil
	}

	var missing *config.MissingConfigError
	if errors.As(err, &missing) {
		return []agents.ID{agents.AgentClaude, agents.AgentCodex, agents.AgentGemini}, agents.ExecutionSettings{}, nil
	}

	return nil, agents.ExecutionSettings{}, err
}
