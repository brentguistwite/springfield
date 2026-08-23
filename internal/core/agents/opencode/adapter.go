// Package opencode adapts the OpenCode CLI ("opencode run") for Springfield
// batch execution, mirroring the gemini adapter's fail-closed control-plane
// injection posture.
package opencode

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"sync"

	"springfield/internal/core/agents"
	coreexec "springfield/internal/core/exec"
)

// Options configures optional adapter behaviour.
type Options struct {
	// WarnWriter receives the one-time warning emitted when no opencode auth
	// source can be detected. Defaults to os.Stderr when nil.
	WarnWriter io.Writer
}

type adapter struct {
	lookPath agents.LookPathFunc

	// hookBin is the absolute path to the springfield binary invoked by the
	// guard plugin installed under <workdir>/.springfield/opencode/.
	hookBin string

	warnBuf io.Writer

	// authWarnOnce guards the one-time "no stored opencode auth / no
	// provider key" warning.
	authWarnOnce sync.Once
}

// The runtime discovers capabilities by optional type assertion, so a dropped
// method would silently disable the capability rather than fail a build. Pin
// what exists after this section; later sections add their own pins.
var _ agents.Commander = (*adapter)(nil)

// New constructs an opencode adapter with default options. Returns an
// agents.Commander so the runtime can build runnable commands.
func New(lookPath agents.LookPathFunc) agents.Commander {
	return NewWithOptions(lookPath, Options{})
}

// NewWithOptions constructs an opencode adapter with optional behaviour
// overrides (e.g. injecting a custom warn writer for tests).
func NewWithOptions(lookPath agents.LookPathFunc, opts Options) agents.Commander {
	if lookPath == nil {
		lookPath = osexec.LookPath
	}

	hookBin, err := os.Executable()
	if err != nil || hookBin == "" {
		// Fallback: trust PATH at hook-run time. Non-fatal — matches the
		// Claude/Gemini adapters' posture.
		hookBin = "springfield"
	}

	warnBuf := opts.WarnWriter
	if warnBuf == nil {
		warnBuf = os.Stderr
	}

	return &adapter{
		lookPath: lookPath,
		hookBin:  hookBin,
		warnBuf:  warnBuf,
	}
}

func (a *adapter) ID() agents.ID {
	return agents.AgentOpenCode
}

func (a *adapter) Metadata() agents.Metadata {
	return agents.Metadata{
		ID:           agents.AgentOpenCode,
		Name:         "OpenCode",
		Binary:       "opencode",
		Capabilities: agents.CapabilitySet{},
	}
}

func (a *adapter) Detect(context.Context) agents.Detection {
	metadata := a.Metadata()
	path, err := a.lookPath(metadata.Binary)

	result := agents.Detection{
		ID:     metadata.ID,
		Name:   metadata.Name,
		Binary: metadata.Binary,
		Path:   path,
		Err:    err,
	}

	switch {
	case err == nil:
		result.Status = agents.DetectionStatusAvailable
	case errors.Is(err, osexec.ErrNotFound):
		result.Status = agents.DetectionStatusMissing
	default:
		result.Status = agents.DetectionStatusUnhealthy
	}

	return result
}

// Command builds a runnable opencode invocation.
//
// IMPORTANT: the prompt rides Stdin, never argv — opencode's non-interactive
// mode reads piped stdin and appends it to the message, so a 256 KB context_md
// payload cannot hit ARG_MAX. No -c/-s/--fork resume flags: Springfield runs a
// fresh process per iteration by design.
func (a *adapter) Command(input agents.CommandInput) (coreexec.Command, error) {
	args := []string{"run", "--format", "json", "--auto"}
	if m := strings.TrimSpace(input.ExecutionSettings.OpenCode.Model); m != "" {
		args = append(args, "--model", m)
	}

	env, err := a.commandEnv(input)
	if err != nil {
		// Fail closed — never spawn opencode without the guard plugin.
		return coreexec.Command{}, err
	}

	a.maybeEmitAuthWarning()

	return coreexec.Command{
		Name:  "opencode",
		Args:  args,
		Stdin: input.Prompt,
		Dir:   input.WorkDir,
		Env:   env,
	}, nil
}

// maybeEmitAuthWarning warns once (per adapter instance) when no opencode auth
// source is detectable. Non-blocking — the subprocess still runs and may pick
// up auth we couldn't see (e.g. a provider plugin reading its own env).
func (a *adapter) maybeEmitAuthWarning() {
	for _, key := range opencodeProviderEnvKeys {
		if os.Getenv(key) != "" {
			return
		}
	}
	home, err := os.UserHomeDir()
	if err == nil {
		if _, statErr := os.Stat(filepath.Join(home, ".local", "share", "opencode", "auth.json")); statErr == nil {
			return
		}
	}
	a.authWarnOnce.Do(func() {
		fmt.Fprintln(a.warnBuf,
			"springfield: no provider API key env vars set and no stored opencode auth at ~/.local/share/opencode/auth.json — subprocess may fail to reach a model",
		)
	})
}

// opencodeProviderEnvKeys are the obvious provider key variables opencode's
// model providers read directly from the environment.
var opencodeProviderEnvKeys = []string{
	"ANTHROPIC_API_KEY",
	"OPENAI_API_KEY",
	"GEMINI_API_KEY",
	"GOOGLE_API_KEY",
	"XAI_API_KEY",
	"OPENROUTER_API_KEY",
	"GROQ_API_KEY",
	"MISTRAL_API_KEY",
	"AZURE_OPENAI_API_KEY",
	"GITHUB_TOKEN",
	"GH_TOKEN",
}
