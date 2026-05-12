package cmd

// Interactive init form built on charmbracelet/huh. The form runs in two
// passes so the per-agent model selects are built dynamically from the
// agent-multi-select answer: pass 1 picks agents, pass 2 collects models +
// shows a summary the operator can review and edit before write.
//
// Non-TTY callers (CI pipes, scripted installs) reach the same fields via
// huh's accessible mode (`huh.WithAccessible(true)`) — that path renders
// plain-text Q&A and parses one answer per line from stdin. This file does
// not maintain a separate non-TTY codepath.

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/huh"

	"springfield/internal/core/agents"
)

// runInitForm collects the agent priority and per-agent model overrides from
// an interactive operator via huh. Returns the priority list, the per-agent
// model map (only includes agents whose selection was non-blank), and any
// error from running the form.
//
// accessible toggles huh's plain-text mode for non-TTY callers; when true
// the form renders as numbered prompts readable line-by-line.
func runInitForm(
	in io.Reader,
	out io.Writer,
	det Detector,
	suggest func(agents.ID) []string,
	accessible bool,
) ([]string, map[string]string, error) {
	// Allocate the lineByLineReader once and share it across both forms.
	// Wrapping `in` in a fresh bufio.Reader per form would let the first
	// form's bufio buffer bytes that the second form then can't see —
	// every byte read past form 1's last prompt would be stranded when
	// form 1's reader is discarded.
	var formIn io.Reader = in
	if accessible && in != nil {
		formIn = &lineByLineReader{r: bufio.NewReader(in)}
	}

	supported := agents.SupportedForExecution()

	options := make([]huh.Option[string], 0, len(supported))
	for _, id := range supported {
		marker := agentDetectionMarker(det.Detect(id))
		label := fmt.Sprintf("%s — %s", id, marker)
		options = append(options, huh.NewOption(label, string(id)))
	}

	var selected []string
	pickAgents := huh.NewForm(
		huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title("Which agents should Springfield use?").
				Description("Pick one or more. Order = priority (first = default).").
				Options(options...).
				Validate(func(picks []string) error {
					if len(picks) == 0 {
						return fmt.Errorf("at least one agent is required")
					}
					return nil
				}).
				Value(&selected),
		),
	)
	pickAgents = configureForm(pickAgents, formIn, out, accessible)
	if err := pickAgents.Run(); err != nil {
		return nil, nil, fmt.Errorf("agent selection: %w", err)
	}

	priority := preservedOrder(selected, supported)
	if len(priority) == 0 {
		return nil, nil, fmt.Errorf("at least one agent is required")
	}

	// Pass 2: per-agent model + summary/confirm. Targets are stable strings
	// across the form lifetime so huh can bind value-passing.
	modelTargets := make(map[string]*string, len(priority))
	for _, id := range priority {
		var model string
		modelTargets[id] = &model
	}

	// Bound the edit loop. Operators can revise their selections by hitting
	// "Edit" on the confirm screen; cap the iterations so a misconfigured
	// pipe stream cannot wedge init in an unbounded re-prompt loop.
	const maxEditLoops = 5
	for attempt := 0; attempt < maxEditLoops; attempt++ {
		groups := make([]*huh.Group, 0, len(priority)+1)
		for _, id := range priority {
			groups = append(groups, modelGroupForAgent(agents.ID(id), modelTargets[id], suggest))
		}

		var confirmed bool
		groups = append(groups, huh.NewGroup(
			huh.NewConfirm().
				Title("Write springfield.toml with these settings?").
				DescriptionFunc(func() string {
					return renderInitSummary(priority, collectModels(priority, modelTargets))
				}, modelTargets).
				Affirmative("Write").
				Negative("Edit").
				Value(&confirmed),
		))

		modelForm := huh.NewForm(groups...)
		modelForm = configureForm(modelForm, formIn, out, accessible)
		if err := modelForm.Run(); err != nil {
			return nil, nil, fmt.Errorf("model selection: %w", err)
		}
		if confirmed {
			return priority, collectModels(priority, modelTargets), nil
		}
		// Operator hit "Edit": loop and re-run the model-selection groups
		// with their current values preserved (the *string accessors keep
		// state across iterations).
	}

	return nil, nil, fmt.Errorf("too many edit cycles; aborting")
}

// modelGroupForAgent builds a Select of "(adapter default)" + suggested
// models for one agent. Custom model ids are intentionally NOT a sentinel +
// follow-up input: huh's accessible mode iterates groups without honoring
// Hide functions (form.go runAccessible), so any conditional second field
// would always prompt on piped stdin and break the answer-script contract.
// Operators wanting an off-list model id pass --model on the command line.
func modelGroupForAgent(id agents.ID, model *string, suggest func(agents.ID) []string) *huh.Group {
	suggestions := suggest(id)

	options := make([]huh.Option[string], 0, len(suggestions)+1)
	options = append(options, huh.NewOption("(use adapter default)", ""))
	for _, s := range suggestions {
		options = append(options, huh.NewOption(s, s))
	}

	return huh.NewGroup(
		huh.NewSelect[string]().
			Title(fmt.Sprintf("Model for %s", id)).
			Description("Pick a suggestion or take the adapter default. Off-list ids: re-run with --model agent=id.").
			Options(options...).
			Value(model),
	)
}

// collectModels reads each model target and returns a per-agent map of
// non-blank selections. Blanks mean "use adapter default" and are omitted.
func collectModels(priority []string, modelTargets map[string]*string) map[string]string {
	out := make(map[string]string, len(priority))
	for _, id := range priority {
		chosen := strings.TrimSpace(*modelTargets[id])
		if chosen != "" {
			out[id] = chosen
		}
	}
	return out
}

// renderInitSummary produces the multi-line description for the confirm
// step, showing the operator every answer they're about to commit. Models
// not in the map render as "(adapter default)".
func renderInitSummary(priority []string, models map[string]string) string {
	var b strings.Builder
	b.WriteString("Priority: ")
	b.WriteString(strings.Join(priority, ", "))
	b.WriteString("\n\n")
	b.WriteString("Models:\n")
	for _, id := range priority {
		model := models[id]
		if model == "" {
			model = "(adapter default)"
		}
		fmt.Fprintf(&b, "  %s = %s\n", id, model)
	}
	return b.String()
}

// preservedOrder returns the items of selected in the order they appear in
// preferred. huh's MultiSelect returns picks in toggle order, but the
// supported-agents order is the canonical priority order — we want the
// final priority list to match that canonical order rather than the click
// order, so two operators picking the same set end up with identical
// configs regardless of toggle sequence.
func preservedOrder(selected []string, preferred []agents.ID) []string {
	picked := make(map[string]struct{}, len(selected))
	for _, s := range selected {
		picked[s] = struct{}{}
	}
	out := make([]string, 0, len(selected))
	for _, id := range preferred {
		if _, ok := picked[string(id)]; ok {
			out = append(out, string(id))
		}
	}
	return out
}

// agentDetectionMarker renders the short human marker for a detection
// status — same vocabulary the prior line-prompt picker used so users
// migrating from older versions see familiar signals.
func agentDetectionMarker(s agents.DetectionStatus) string {
	switch s {
	case agents.DetectionStatusAvailable:
		return "✓ detected on PATH"
	case agents.DetectionStatusUnhealthy:
		return "⚠ found but unhealthy"
	default:
		return "✗ not found"
	}
}

// configureForm wires the io plumbing and accessible-mode toggle onto a
// huh.Form. The caller is responsible for handing in a stable reader
// shared across all forms in a sequence (see runInitForm for the why).
//
// Accessible-mode input arrives via lineByLineReader: huh's
// internal/accessibility.PromptString constructs a fresh bufio.Scanner per
// call and reads a 4KB block on the first Scan(), which drains all queued
// piped lines into the first prompt and starves every subsequent prompt
// with EOF. lineByLineReader caps each Read at one line so each Scanner
// only buffers one line and the next prompt finds its line still on the
// underlying reader.
func configureForm(form *huh.Form, in io.Reader, out io.Writer, accessible bool) *huh.Form {
	form = form.WithAccessible(accessible)
	if in != nil {
		form = form.WithInput(in)
	}
	if out != nil {
		form = form.WithOutput(out)
	}
	return form
}

// maxLineReaderEOFs bounds how many consecutive EOF reads the
// lineByLineReader tolerates before bailing out. huh's accessible-mode
// loops re-prompt on every PromptInt returning 0 from EOF, and huh swallows
// io.Reader errors — so without a forced exit an empty or already-drained
// stdin would wedge the form in an infinite re-prompt loop. The threshold
// is high enough that a normal multi-prompt sequence does not trip it
// during legitimate input lulls, but small enough to bail within seconds
// when the pipe is genuinely dead.
const maxLineReaderEOFs = 16

// lineByLineReader is an io.Reader that returns at most one line per Read.
// See configureForm for the why; this is the smallest workaround that does
// not require forking huh.
//
// After maxLineReaderEOFs consecutive EOF reads the reader calls the
// exhausted hook (default: os.Exit) so the wedged accessible-mode loop
// cannot spin forever. Tests inject a hook that records the exit instead.
// This replaces the earlier goroutine-based Peek probe, which raced with
// huh's later reads on the same *bufio.Reader (round-3 review finding).
type lineByLineReader struct {
	r         *bufio.Reader
	eofs      int
	onExhaust func()
}

func (l *lineByLineReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	n := 0
	for n < len(p) {
		b, err := l.r.ReadByte()
		if err != nil {
			if n > 0 {
				l.eofs = 0
				return n, nil
			}
			l.eofs++
			if l.eofs >= maxLineReaderEOFs {
				if l.onExhaust != nil {
					l.onExhaust()
				} else {
					fmt.Fprintln(os.Stderr, "init: stdin exhausted before form completed — pass --agents or pipe enough answers for every prompt")
					os.Exit(2)
				}
			}
			return 0, err
		}
		l.eofs = 0
		p[n] = b
		n++
		if b == '\n' {
			return n, nil
		}
		// CR-only line ending (old Mac, some Windows tools that emit \r
		// without \n): treat as terminator. If the next byte is \n, swallow
		// it so the answer parses as one logical line and the subsequent
		// prompt's scanner does not see a stray empty line.
		if b == '\r' {
			next, perr := l.r.Peek(1)
			if perr == nil && len(next) == 1 && next[0] == '\n' {
				_, _ = l.r.ReadByte()
			}
			return n, nil
		}
	}
	return n, nil
}
