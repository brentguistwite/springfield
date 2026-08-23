package agents_test

import (
	"encoding/json"
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
// installed plugin bytes through a JS runtime: SpringfieldGuard's
// tool.execute.before must THROW for a known-mutating call whose arg shape
// yields no path/command (fail closed), never fall through to allow.
func TestOpenCodeGuardPluginDeniesUnextractableMutatingCall(t *testing.T) {
	runtime, ok := jsRuntime()
	if !ok {
		t.Skip("no bun/node JS runtime on PATH; fail-closed JS behavior pinned only where a runtime exists")
	}

	plugin := guardPluginSource(t)
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

	cmd := exec.Command(runtime, driverPath, pluginPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("JS harness failed (%v):\n%s", err, out)
	}
	if !strings.Contains(string(out), "OK: 4 unextractable mutating calls denied") {
		t.Fatalf("unexpected harness output:\n%s", out)
	}
}

// TestOpenCodeGuardPluginFailsClosedOnHookTransportFailure drives the ACTUAL
// installed plugin bytes through a JS runtime: when the hook binary cannot be
// resolved/executed (SPRINGFIELD_HOOK_BIN points nowhere), tool.execute.before
// must DENY (throw) a benign bash call instead of silently allowing it because
// the guard never ran. A hook binary that runs and exits 0 still permits.
func TestOpenCodeGuardPluginFailsClosedOnHookTransportFailure(t *testing.T) {
	runtime, ok := jsRuntime()
	if !ok {
		t.Skip("no bun/node JS runtime on PATH; fail-closed JS behavior pinned only where a runtime exists")
	}

	plugin := guardPluginSource(t)
	dir := t.TempDir()
	pluginPath := filepath.Join(dir, "springfield-guard.js")
	if err := os.WriteFile(pluginPath, []byte(plugin), 0o600); err != nil {
		t.Fatalf("write plugin copy: %v", err)
	}

	// Driver: import the real plugin, invoke tool.execute.before with a
	// benign bash command under the ambient SPRINGFIELD_HOOK_BIN, and print
	// ALLOWED (guard permitted) or DENIED (guard threw).
	driver := `
import { pathToFileURL } from "node:url"
const mod = await import(pathToFileURL(process.argv[2]).href)
const guard = await mod.SpringfieldGuard({})
try {
    await guard["tool.execute.before"]({ tool: "bash" }, { args: { command: "echo hi" } })
    console.log("ALLOWED")
} catch (err) {
    console.log("DENIED: " + String(err))
}
`
	driverPath := filepath.Join(dir, "guard-transport-driver.mjs")
	if err := os.WriteFile(driverPath, []byte(driver), 0o600); err != nil {
		t.Fatalf("write driver: %v", err)
	}

	runDriver := func(hookBin string) string {
		t.Helper()
		cmd := exec.Command(runtime, driverPath, pluginPath)
		cmd.Env = append(os.Environ(), "SPRINGFIELD_HOOK_BIN="+hookBin)
		out, _ := cmd.CombinedOutput()
		return string(out)
	}

	allowHook := filepath.Join(dir, "allow-hook")
	if err := os.WriteFile(allowHook, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write allow hook: %v", err)
	}

	if out := runDriver(allowHook); !strings.Contains(out, "ALLOWED") {
		t.Fatalf("status-0 hook must permit the call; got:\n%s", out)
	}

	out := runDriver("/nonexistent/springfield-path")
	if strings.Contains(out, "ALLOWED") || !strings.Contains(out, "DENIED") {
		t.Fatalf("hook-transport failure must DENY (throw), not fail open; got:\n%s", out)
	}
}

func jsRuntime() (string, bool) {
	for _, name := range []string{"bun", "node"} {
		if path, err := exec.LookPath(name); err == nil {
			return path, true
		}
	}
	return "", false
}
