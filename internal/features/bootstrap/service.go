package bootstrap

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
	"springfield/internal/features/doctor"
	"springfield/internal/features/execution"
	"springfield/internal/storage"
)

type Status struct {
	WorkingDir          string
	ProjectRoot         string
	ConfigPath          string
	RuntimeDir          string
	ExecutionConfigPath string
	ConfigPresent       bool
	RuntimePresent      bool
	ExecutionReady      bool
	Error               string
}

func (s Status) NeedsInit() bool {
	return !s.ConfigPresent || !s.RuntimePresent
}

type ExecutionConfigInput struct {
	PlansDir                   string
	WorktreeBase               string
	MaxRetries                 int
	SingleWorkstreamIterations int
	SingleWorkstreamTimeout    int
}

type ExecutionConfigResult struct {
	Created bool
	Reused  bool
	Path    string
}

type ExecutionConfig struct {
	PlansDir                   string
	WorktreeBase               string
	MaxRetries                 int
	SingleWorkstreamIterations int
	SingleWorkstreamTimeout    int
}

type AgentDetection struct {
	ID        string
	Name      string
	Installed bool
}

type AgentExecutionModes struct {
	Claude string
	Codex  string
}

type SaveAgentExecutionModesInput struct {
	Claude string
	Codex  string
}

type Service struct {
	cwd      func() (string, error)
	lookPath func(string) (string, error)
}

func NewService(cwd func() (string, error), lookPath func(string) (string, error)) Service {
	if cwd == nil {
		cwd = os.Getwd
	}
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	return Service{cwd: cwd, lookPath: lookPath}
}

func (s Service) Status() Status {
	workingDir, err := s.cwd()
	if err != nil {
		return Status{Error: err.Error()}
	}

	status := Status{
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

	status.ExecutionConfigPath = filepath.Join(status.RuntimeDir, "execution", "config.json")
	if info, err := os.Stat(status.ExecutionConfigPath); err == nil && !info.IsDir() {
		if _, err := execution.Load(status.ProjectRoot); err == nil {
			status.ExecutionReady = true
		}
	}

	return status
}

func (s Service) InitProject() (config.InitResult, error) {
	status := s.Status()
	targetDir := status.ProjectRoot
	if targetDir == "" {
		targetDir = status.WorkingDir
	}
	return config.Init(targetDir)
}

func (s Service) ConfigureExecution(input ExecutionConfigInput) (ExecutionConfigResult, error) {
	status := s.Status()
	if status.Error != "" {
		return ExecutionConfigResult{}, errors.New(status.Error)
	}
	if !status.ConfigPresent || !status.RuntimePresent {
		return ExecutionConfigResult{}, errors.New("run Guided Setup first to initialize the project")
	}

	loaded, err := config.LoadFrom(status.ProjectRoot)
	if err != nil {
		return ExecutionConfigResult{}, err
	}

	result, err := execution.Setup(status.ProjectRoot, loaded.Config.EffectivePriority(), execution.Input{
		PlansDir:                   input.PlansDir,
		WorktreeBase:               input.WorktreeBase,
		MaxRetries:                 input.MaxRetries,
		SingleWorkstreamIterations: input.SingleWorkstreamIterations,
		SingleWorkstreamTimeout:    input.SingleWorkstreamTimeout,
	})
	if err != nil {
		return ExecutionConfigResult{}, err
	}

	return ExecutionConfigResult{
		Created: result.Created,
		Reused:  result.Reused,
		Path:    result.Path,
	}, nil
}

func (s Service) DetectAgents() []AgentDetection {
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

func (s Service) AgentPriority() []string {
	status := s.Status()
	if !status.ConfigPresent {
		return nil
	}
	loaded, err := config.LoadFrom(status.ProjectRoot)
	if err != nil {
		return nil
	}
	return loaded.Config.EffectivePriority()
}

func (s Service) AgentExecutionModes() AgentExecutionModes {
	status := s.Status()
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

func (s Service) CurrentExecutionConfig() *ExecutionConfig {
	status := s.Status()
	if !status.ExecutionReady {
		return nil
	}
	project, err := execution.Load(status.ProjectRoot)
	if err != nil {
		return nil
	}
	return &ExecutionConfig{
		PlansDir:                   project.PlansDir,
		WorktreeBase:               project.WorktreeBase,
		MaxRetries:                 project.MaxRetries,
		SingleWorkstreamIterations: project.SingleWorkstreamIterations,
		SingleWorkstreamTimeout:    project.SingleWorkstreamTimeout,
	}
}

func (s Service) SaveAgentPriority(priority []string) error {
	status := s.Status()
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

func (s Service) SaveAgentExecutionModes(input SaveAgentExecutionModesInput) error {
	status := s.Status()
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

func (s Service) EnsureRecommendedExecutionDefaults() error {
	status := s.Status()
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

func (s Service) UpdateExecutionConfig(input ExecutionConfigInput) (ExecutionConfigResult, error) {
	status := s.Status()
	if status.Error != "" {
		return ExecutionConfigResult{}, errors.New(status.Error)
	}
	loaded, err := config.LoadFrom(status.ProjectRoot)
	if err != nil {
		return ExecutionConfigResult{}, err
	}
	result, err := execution.Update(status.ProjectRoot, loaded.Config.EffectivePriority(), execution.Input{
		PlansDir:                   input.PlansDir,
		WorktreeBase:               input.WorktreeBase,
		MaxRetries:                 input.MaxRetries,
		SingleWorkstreamIterations: input.SingleWorkstreamIterations,
		SingleWorkstreamTimeout:    input.SingleWorkstreamTimeout,
	})
	if err != nil {
		return ExecutionConfigResult{}, err
	}
	return ExecutionConfigResult{
		Created: false,
		Reused:  false,
		Path:    result.Path,
	}, nil
}

func (s Service) DoctorSummary() doctor.Report {
	registry := agents.NewRegistry(
		claude.New(s.lookPath),
		codex.New(s.lookPath),
		gemini.New(s.lookPath),
	)
	return doctor.Run(context.Background(), registry)
}
