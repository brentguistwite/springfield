package agents_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/core/agents"
	"springfield/internal/core/agents/opencode"
	coreexec "springfield/internal/core/exec"
)

// C.8 fail-closed invariant: the guard plugin's arg extraction must match the
// REAL tool_use part.state.input key names opencode emits — pinned here
// against the captured corpus — and the plugin must DENY (throw) a known-
// mutating call whose args yield no path/command. A wrong-key regression
// (opencode rename OR plugin edit) fails this test instead of silently
// failing open at runtime.

// guardPluginSource returns the exact bytes the adapter installs as the guard
// plugin (obtained by running Command, which writes them from the in-package
// source constant).
func guardPluginSource(t *testing.T) string {
	t.Helper()
	a := opencode.New(func(string) (string, error) { return "/opt/bin/opencode", nil })
	workDir := t.TempDir()
	if _, err := a.(agents.Commander).Command(agents.CommandInput{Prompt: "hi", WorkDir: workDir}); err != nil {
		t.Fatalf("Command err: %v", err)
	}
	plugin, err := os.ReadFile(filepath.Join(workDir, ".springfield", "opencode", "plugins", "springfield-guard.js"))
	if err != nil {
		t.Fatalf("read guard plugin: %v", err)
	}
	return string(plugin)
}

// guardProbeEvent mirrors only the tool_use fields the probe consumes (outer
// type authoritative — see ValidateResult doc comment).
type guardProbeEvent struct {
	Type string `json:"type"`
	Part struct {
		Tool  string `json:"tool"`
		State struct {
			Status string          `json:"status"`
			Input  json.RawMessage `json:"input"`
		} `json:"state"`
	} `json:"part"`
}

// pluginReadKeys is the plugin's extraction surface per tool: the member
// names firstString(...) reads off output.args. Mirrored verbatim from
// springfield-guard.js — the assertion below cross-checks the plugin SOURCE
// still references every listed key, so drift in either direction fails.
var pluginReadKeys = map[string][]string{
	"bash":  {"command"},
	"write": {"filePath", "file_path", "path"},
	"edit":  {"filePath", "file_path", "path"},
	"patch": {"filePath", "file_path", "path"},
	"read":  {"filePath", "file_path", "path"},
}

// TestOpenCodeGuardPluginExtractsCapturedArgKeys decodes every captured
// tool_use event's part.state.input and asserts the keys the plugin actually
// reads include the ones present in real opencode traffic. If opencode ever
// renames a key, re-captures land here and this test goes red BEFORE the
// guard starts denying legitimate agent work (fail-closed tripwire).
func TestOpenCodeGuardPluginExtractsCapturedArgKeys(t *testing.T) {
	plugin := guardPluginSource(t)

	for tool, keys := range pluginReadKeys {
		for _, key := range keys {
			expr := "a." + key
			if !strings.Contains(plugin, expr) {
				t.Errorf("guard plugin no longer reads %s (tool %q); update pluginReadKeys or fix the plugin", expr, tool)
			}
		}
	}

	corpus := []string{
		filepath.Join("fixtures", "opencode", "success.jsonl"),
		filepath.Join("fixtures", "opencode", "tool-error.jsonl"),
		filepath.Join("..", "realcaptures", "opencode", "edit-capture.jsonl"),
	}

	seen := map[string]bool{}
	for _, rel := range corpus {
		for _, e := range loadFixtureEvents(t, rel) {
			if e.Type != coreexec.EventStdout {
				continue
			}
			var ev guardProbeEvent
			if err := json.Unmarshal([]byte(e.Data), &ev); err != nil {
				continue
			}
			if ev.Type != "tool_use" || ev.Part.Tool == "" {
				continue
			}
			seen[ev.Part.Tool] = true

			readable, ok := pluginReadKeys[ev.Part.Tool]
			if !ok {
				t.Logf("corpus carries tool %q with no guard mapping (informational)", ev.Part.Tool)
				continue
			}
			var input map[string]any
			if err := json.Unmarshal(ev.Part.State.Input, &input); err != nil {
				t.Fatalf("%s: tool %q state.input is not an object: %v", rel, ev.Part.Tool, err)
			}
			matched := false
			for _, key := range readable {
				if _, ok := input[key]; ok {
					matched = true
				}
			}
			if !matched {
				t.Errorf("%s: tool %q input keys %v share NO key with the plugin's extraction surface %v — the guard would refuse this real call",
					rel, ev.Part.Tool, keysOf(input), readable)
			}
		}
	}

	for _, tool := range []string{"write", "edit", "bash", "read"} {
		if !seen[tool] {
			t.Errorf("corpus never exercises tool %q — the key pin for it is vacuous; extend the captures", tool)
		}
	}
}

func keysOf(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// TestOpenCodeGuardPluginDeniesUnextractableMutatingCall drives the ACTUAL
// installed plugin bytes through EVERY JS runtime available (bun AND node —
// local dev and CI must exercise the same code paths, not whichever runtime
// happens to sort first on PATH): SpringfieldGuard's tool.execute.before must
// THROW for a known-mutating call whose arg shape yields no path/command
// (fail closed), never fall through to allow.
func TestOpenCodeGuardPluginDeniesUnextractableMutatingCall(t *testing.T) {
	runtimes := requireJSRuntimes(t)
	plugin := guardPluginSource(t)

	for _, rt := range runtimes {
		t.Run(rt.name, func(t *testing.T) {
			dir := t.TempDir()
			pluginPath := filepath.Join(dir, "springfield-guard.js")
			if err := os.WriteFile(pluginPath, []byte(plugin), 0o600); err != nil {
				t.Fatalf("write plugin copy: %v", err)
			}

			// Driver: import the real plugin, invoke tool.execute.before with
			// synthetic known-mutating calls whose args contain no extractable
			// path/command. Pass iff every call THROWS (deny).
			driver := `
import { pathToFileURL } from "node:url"
const mod = await import(pathToFileURL(process.argv[2]).href)
const guard = await mod.SpringfieldGuard({})
let denied = 0
for (const [tool, args] of [
    ["write", { someJunk: "no path here" }],
    ["edit", {}],
    ["patch", undefined],
    ["bash", { cwd: "/tmp", timeout: 5 }],
]) {
    try {
        const output = { args }
        await guard["tool.execute.before"]({ tool }, output)
        console.error("ALLOWED (must deny): " + tool + " args=" + JSON.stringify(args))
        process.exit(1)
    } catch (err) {
        if (!/springfield-guard/.test(String(err))) {
            console.error("threw non-guard error for " + tool + ": " + err)
            process.exit(1)
        }
        denied++
    }
}
console.log("OK: " + denied + " unextractable mutating calls denied")
`
			driverPath := filepath.Join(dir, "guard-deny-driver.mjs")
			if err := os.WriteFile(driverPath, []byte(driver), 0o600); err != nil {
				t.Fatalf("write driver: %v", err)
			}

			cmd := exec.Command(rt.path, driverPath, pluginPath)
			cmd.Env = guardDriverEnv("")
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("JS harness failed (%v):\n%s", err, out)
			}
			if !strings.Contains(string(out), "OK: 4 unextractable mutating calls denied") {
				t.Fatalf("unexpected harness output:\n%s", out)
			}
		})
	}
}

// TestOpenCodeGuardPluginFailsClosedOnHookTransportFailure drives the ACTUAL
// installed plugin bytes through EVERY available JS runtime (see the deny
// test above — local and CI must not exercise different runtimes). Scenarios:
//
//  1. hook binary unresolvable → tool.execute.before must DENY (throw), never
//     silently allow because the guard never ran.
//  2. hook runs and exits 0 → must PERMIT, even when it exits without
//     draining stdin. The oversized payload (far larger than the OS pipe
//     buffer) makes the undrained-stdin EPIPE deterministic instead of a
//     scheduler race: spawnSync must still honor the child's exit-0 verdict.
func TestOpenCodeGuardPluginFailsClosedOnHookTransportFailure(t *testing.T) {
	runtimes := requireJSRuntimes(t)
	plugin := guardPluginSource(t)

	for _, rt := range runtimes {
		t.Run(rt.name, func(t *testing.T) {
			dir := t.TempDir()
			pluginPath := filepath.Join(dir, "springfield-guard.js")
			if err := os.WriteFile(pluginPath, []byte(plugin), 0o600); err != nil {
				t.Fatalf("write plugin copy: %v", err)
			}

			// Driver: import the real plugin, invoke tool.execute.before with
			// the bash command from argv[3], and print ALLOWED (guard
			// permitted) or DENIED (guard threw).
			driver := `
import { pathToFileURL } from "node:url"
const mod = await import(pathToFileURL(process.argv[2]).href)
const guard = await mod.SpringfieldGuard({})
const command = process.argv[3] || "echo hi"
try {
    await guard["tool.execute.before"]({ tool: "bash" }, { args: { command } })
    console.log("ALLOWED")
} catch (err) {
    console.log("DENIED: " + String(err))
}
`
			driverPath := filepath.Join(dir, "guard-transport-driver.mjs")
			if err := os.WriteFile(driverPath, []byte(driver), 0o600); err != nil {
				t.Fatalf("write driver: %v", err)
			}

			runDriver := func(hookBin, command string) string {
				t.Helper()
				cmd := exec.Command(rt.path, driverPath, pluginPath, command)
				cmd.Env = guardDriverEnv(hookBin)
				out, _ := cmd.CombinedOutput()
				return string(out)
			}

			allowHook := filepath.Join(dir, "allow-hook")
			if err := os.WriteFile(allowHook, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
				t.Fatalf("write allow hook: %v", err)
			}

			if out := runDriver(allowHook, "echo hi"); !strings.Contains(out, "ALLOWED") {
				t.Fatalf("status-0 hook must permit the call; got:\n%s", out)
			}

			out := runDriver("/nonexistent/springfield-path", "echo hi")
			if strings.Contains(out, "ALLOWED") || !strings.Contains(out, "DENIED") {
				t.Fatalf("hook-transport failure must DENY (throw), not fail open; got:\n%s", out)
			}
		})
	}
}

// TestOpenCodeGuardPluginHookVerdictTruthTable pins hookVerdict's full
// decision table with synthetic spawnSync results across EVERY available JS
// runtime. This is the layer where exotic transport states are pinned
// deterministically: the CI failure this guards against (node on ubuntu
// reports proc.error=EPIPE alongside status=0 when a fast-exiting hook
// doesn't drain stdin) cannot be reproduced on demand through real pipes —
// it's a scheduler race, and forcing it with oversized payloads SIGPIPE-kills
// the driver instead of returning in-band data.
func TestOpenCodeGuardPluginHookVerdictTruthTable(t *testing.T) {
	runtimes := requireJSRuntimes(t)
	plugin := guardPluginSource(t)

	cases := []struct {
		name string
		proc string // JS object literal for the synthetic spawnSync result
		want string // expected outcome field
	}{
		{"clean-exit-0-allows", `{ status: 0, stderr: "" }`, "allow"},
		// The exact CI shape: benign write-side EPIPE + clean exit verdict.
		{"epipe-with-exit-0-still-allows", `{ error: new Error("spawnSync allow-hook EPIPE"), status: 0, stderr: null }`, "allow"},
		{"exit-2-blocks", `{ status: 2, stderr: "blocked: .springfield is protected" }`, "blocked"},
		{"unresolvable-binary-denies", `{ error: new Error("spawnSync ENOENT"), status: null }`, "transport-failed"},
		{"signal-death-denies", `{ error: null, status: null, signal: "SIGKILL" }`, "transport-failed"},
		{"unexpected-code-denies", `{ error: null, status: 1 }`, "transport-failed"},
	}

	for _, rt := range runtimes {
		t.Run(rt.name, func(t *testing.T) {
			dir := t.TempDir()
			pluginPath := filepath.Join(dir, "springfield-guard.js")
			if err := os.WriteFile(pluginPath, []byte(plugin), 0o600); err != nil {
				t.Fatalf("write plugin copy: %v", err)
			}

			var b strings.Builder
			b.WriteString(`
import { pathToFileURL } from "node:url"
const mod = await import(pathToFileURL(process.argv[2]).href)
const cases = [
`)
			for _, tc := range cases {
				fmt.Fprintf(&b, "    { name: %q, proc: %s, want: %q },\n", tc.name, tc.proc, tc.want)
			}
			b.WriteString(`]
let failures = 0
for (const { name, proc, want } of cases) {
    const got = mod.hookVerdict(proc)
    if (got.outcome !== want) {
        console.log("MISMATCH " + name + ": want=" + want + " got=" + JSON.stringify(got))
        failures++
    }
}
if (failures > 0) {
    console.log("FAILED " + failures + "/" + cases.length)
} else {
    console.log("OK: " + cases.length + " verdict cases")
}
`)
			driverPath := filepath.Join(dir, "guard-verdict-driver.mjs")
			if err := os.WriteFile(driverPath, []byte(b.String()), 0o600); err != nil {
				t.Fatalf("write driver: %v", err)
			}

			cmd := exec.Command(rt.path, driverPath, pluginPath)
			cmd.Env = guardDriverEnv("")
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("JS harness failed (%v):\n%s", err, out)
			}
			if !strings.Contains(string(out), fmt.Sprintf("OK: %d verdict cases", len(cases))) {
				t.Fatalf("hookVerdict truth table regressed under %s:\n%s", rt.name, out)
			}
		})
	}
}

type jsRuntime struct{ name, path string }

// jsRuntimeLookPath is the binary resolver behind jsRuntimes, injectable so
// tests can simulate a machine with no JS runtime.
var jsRuntimeLookPath = exec.LookPath

// jsRuntimes returns EVERY distinct JS runtime available. The guard-plugin
// tests deliberately matrix across all of them: picking only the first match
// let local dev (bun) pass while CI (node-only) failed on runtime-specific
// spawn semantics.
func jsRuntimes() []jsRuntime {
	var found []jsRuntime
	seen := map[string]bool{}
	for _, name := range []string{"bun", "node"} {
		path, err := jsRuntimeLookPath(name)
		if err != nil || seen[path] {
			continue
		}
		seen[path] = true
		found = append(found, jsRuntime{name: name, path: path})
	}
	return found
}

// requireJSRuntimes resolves the runtime matrix for guard-plugin tests or
// declares why they won't run. Silent-skip tripwire: with no runtime AND
// CI=true it FAILS instead of skipping — the hookVerdict fail-closed layer
// must never ship unpinned on CI behind a green checkmark. Locally (no CI)
// a missing runtime stays a plain skip.
func requireJSRuntimes(tb testing.TB) []jsRuntime {
	runtimes := jsRuntimes()
	if len(runtimes) > 0 {
		return runtimes
	}
	if os.Getenv("CI") == "true" {
		tb.Fatalf("no bun/node JS runtime on PATH in CI — the hookVerdict fail-closed guard layer would run unpinned; install node in the CI image")
	}
	tb.Skip("no bun/node JS runtime on PATH; fail-closed JS behavior pinned only where a runtime exists")
	return nil
}

// guardDriverEnv builds the MINIMAL environment for guard-plugin driver
// processes. Drivers used to inherit os.Environ(), so ambient machine state
// like NODE_OPTIONS='--require poison.js' killed every node-driven case
// (demonstrated: red locally, green CI — the inverse of the original bug).
// Only what the drivers and runtimes actually need passes through: PATH for
// binary resolution, HOME/TMPDIR for runtime caches and temp files, and
// SPRINGFIELD_HOOK_BIN pointing at the guard binary under test.
func guardDriverEnv(hookBin string) []string {
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"TMPDIR=" + os.Getenv("TMPDIR"),
	}
	if hookBin != "" {
		env = append(env, "SPRINGFIELD_HOOK_BIN="+hookBin)
	}
	return env
}

// recTB is a recording testing.TB fake so the CI tripwire branch of
// requireJSRuntimes can be pinned without spawning a test subprocess.
type recTB struct {
	testing.TB
	fatals []string
	skips  bool
}

func (r *recTB) Helper() {}
func (r *recTB) Fatal(args ...any) {
	r.fatals = append(r.fatals, fmt.Sprint(args...))
}
func (r *recTB) Fatalf(format string, args ...any) {
	r.fatals = append(r.fatals, fmt.Sprintf(format, args...))
}
func (r *recTB) Skip(args ...any) { r.skips = true }
func (r *recTB) Skipf(format string, args ...any) {
	r.skips = true
}

// TestRequireJSRuntimesFailsLoudlyInCIWithoutRuntime pins the silent-skip
// tripwire: with NO JS runtime resolvable AND CI=true, requireJSRuntimes must
// FAIL rather than skip — a silent skip would leave the entire hookVerdict
// fail-closed layer unpinned on CI behind a green checkmark.
func TestRequireJSRuntimesFailsLoudlyInCIWithoutRuntime(t *testing.T) {
	t.Run("no runtime in CI fails loudly", func(t *testing.T) {
		t.Setenv("CI", "true")
		origLookPath := jsRuntimeLookPath
		jsRuntimeLookPath = func(string) (string, error) { return "", exec.ErrNotFound }
		t.Cleanup(func() { jsRuntimeLookPath = origLookPath })

		tb := &recTB{TB: t}
		got := requireJSRuntimes(tb)
		if len(got) != 0 {
			t.Fatalf("expected no runtimes, got %v", got)
		}
		if len(tb.fatals) == 0 {
			t.Fatal("requireJSRuntimes neither failed nor returned runtimes in CI with no JS runtime")
		}
	})
	t.Run("no runtime locally still skips", func(t *testing.T) {
		origLookPath := jsRuntimeLookPath
		jsRuntimeLookPath = func(string) (string, error) { return "", exec.ErrNotFound }
		t.Cleanup(func() { jsRuntimeLookPath = origLookPath })
		t.Setenv("CI", "")

		tb := &recTB{TB: t}
		got := requireJSRuntimes(tb)
		if len(got) != 0 || !tb.skips || len(tb.fatals) != 0 {
			t.Fatalf("local no-runtime should plain-skip; got runtimes=%v skips=%v fatals=%v", got, tb.skips, tb.fatals)
		}
	})
}
