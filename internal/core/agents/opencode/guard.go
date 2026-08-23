package opencode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"springfield/internal/core/agents"
)

// controlPlaneDirName is Springfield's runtime state directory, created by the
// springfield binary itself at spawn time. The agent-facing hook-guard blocks
// *agent* writes into it; springfield writing here is the same privilege the
// gemini adapter exercises when it drops its system-settings override.
const controlPlaneDirName = ".springfield"

// pluginRelPath is the guard plugin's location relative to the injected
// OPENCODE_CONFIG_DIR.
const pluginRelPath = "plugins/springfield-guard.js"

// pluginSource is the fail-closed tool.execute.before guard plugin, verbatim
// from the approved plan. It translates opencode tool calls into hook-guard
// payloads and throws to deny; see the file header for the verified target.
// (The one Bun template literal is stitched in around Go raw-string limits.)
//
// Exact arg key names get pinned by fixtures in Section C; firstString ships
// the multi-key fallback shape so a wrong guess fails CLOSED.
var pluginSource = buildPluginSource()

func buildPluginSource() string {
	return `// springfield-guard.js — Springfield control-plane guard for OpenCode.
// Target: OpenCode CLI v1.18.21 (verified 2026-08-22): a local plugin under
// OPENCODE_CONFIG_DIR/plugins exposing "tool.execute.before" that throws to
// block a call. Managed by Springfield — regenerated per invocation.
//
// NOTE: runs the guard via node:child_process.spawnSync, NOT Bun's $ shell —
// verified 2026-08-22 that the $ ResponsePromise in opencode v1.18.21 has no
// .stdin() method (every tool call errored with "stdin is not a function").
// spawnSync is Node-stdlib, so the plugin stays dependency-free, and argv-array
// spawning is correct for binary paths containing spaces.
import { spawnSync } from "node:child_process"
export const SpringfieldGuard = async () => {
    const bin = process.env.SPRINGFIELD_HOOK_BIN || "springfield"
    const runGuard = (toolInput) => {
        const payload = JSON.stringify({ tool_input: toolInput })
        // --block-reentry: subagent context — block all springfield
        // start/plan/recover (see cmd/hook_guard.go).
        const proc = spawnSync(bin, ["hook-guard", "--block-reentry"], {
            input: payload,
            encoding: "utf8",
        })
        // Fail CLOSED on transport failure: ONLY an unambiguous "guard ran
        // and allowed" (exit status 0) permits. Check STATUS FIRST — a clean
        // exit 0 is the allow verdict even when proc.error is set: a hook
        // that exits without draining our stdin yields a benign EPIPE on the
        // write side (observed under node on CI, where /bin/sh exits faster
        // than the payload write lands). Everything else — a spawn error
        // where no status exists (binary unresolvable/not executable), death
        // by signal (null status), exit 2 (deny), or any unexpected code —
        // denies. An unguarded tool call must never slip through.
        if (proc.status === 0) return
        if (proc.status === 2) throw new Error(proc.stderr || "springfield-guard: blocked")
        throw new Error("springfield-guard: hook transport failed (" + (proc.error || "unexpected exit status " + proc.status) + ") — denying")
    }
    // Fail CLOSED: for a known-mutating tool, if we cannot extract the
    // path/command arg (wrong/renamed opencode key), throw rather than
    // pass through. hook-guard fails OPEN on a missing tool_input field
    // (type-assert miss = "no match, allow"), so a silently-undefined key
    // here would defeat the guard. An unmapped mutating call must abort.
    const firstString = (...vals) => vals.find((v) => typeof v === "string" && v.length > 0)
    return {
        "tool.execute.before": async (input, output) => {
            const t = input.tool
            const a = output.args ?? {}
            if (t === "bash") {
                const command = firstString(a.command)
                if (command === undefined) throw new Error("springfield-guard: bash call with no command arg — refusing")
                return runGuard({ command })
            }
            if (t === "edit" || t === "write" || t === "patch") {
                const file_path = firstString(a.filePath, a.file_path, a.path)
                if (file_path === undefined) throw new Error(` + "`" + `springfield-guard: ${t} call with no recognizable path arg — refusing` + "`" + `)
                return runGuard({ file_path })
            }
            if (t === "read") {
                const file_path = firstString(a.filePath, a.file_path, a.path)
                if (file_path !== undefined) return runGuard({ file_path })
            }
        },
    }
}
`
}

// guardConfig is the belt-and-suspenders declarative permission layer,
// delivered inline via OPENCODE_CONFIG_CONTENT (which overrides user AND
// project opencode config — the plain OPENCODE_CONFIG file lever loses to
// project config and is deliberately not used).
//
// Field order is pinned by declaration so marshalled bytes are deterministic;
// map keys inside Permission.Edit are sorted by encoding/json.
type guardConfig struct {
	Schema     string             `json:"$schema"`
	Autoupdate bool               `json:"autoupdate"`
	Share      string             `json:"share"`
	Permission permissionSettings `json:"permission"`
}

type permissionSettings struct {
	Edit              editRules `json:"edit"`
	ExternalDirectory string    `json:"external_directory"`
}

type editRules struct {
	All                string `json:"*"`
	SpringfieldDeep    string `json:"**/.springfield/**"`
	SpringfieldShallow string `json:".springfield/**"`
}

func defaultGuardConfig() guardConfig {
	return guardConfig{
		Schema:     "https://opencode.ai/config.json",
		Autoupdate: false, // no mid-batch binary drift
		Share:      "disabled",
		Permission: permissionSettings{
			Edit: editRules{
				All:                "allow",
				SpringfieldDeep:    "deny",
				SpringfieldShallow: "deny",
			},
			ExternalDirectory: "ask",
		},
	}
}

func marshalGuardConfig() ([]byte, error) {
	data, err := json.MarshalIndent(defaultGuardConfig(), "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal opencode guard config: %w", err)
	}
	return data, nil
}

// commandEnv writes the per-invocation guard assets under
// <workdir>/.springfield/opencode/ and returns the env map pointing opencode
// at them:
//
//   - OPENCODE_CONFIG_DIR — searched like .opencode/, loads after it, carries
//     the plugins/ directory with the guard plugin.
//   - OPENCODE_CONFIG_CONTENT — inline JSON config; overrides user AND project
//     config (permission denies + batch-hygiene keys).
//   - SPRINGFIELD_HOOK_BIN — absolute springfield binary the plugin shells out
//     to (`hook-guard --block-reentry`).
//
// Any write failure returns an error → Command refuses to spawn (fail closed).
func (a *adapter) commandEnv(input agents.CommandInput) (map[string]string, error) {
	configDir := filepath.Join(input.WorkDir, controlPlaneDirName, "opencode")
	pluginDir := filepath.Join(configDir, filepath.Dir(pluginRelPath))
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		return nil, fmt.Errorf(
			"opencode adapter: cannot install control-plane hook (guard dir create failed); refusing to spawn: %w",
			err,
		)
	}
	pluginPath := filepath.Join(pluginDir, filepath.Base(pluginRelPath))
	if err := os.WriteFile(pluginPath, []byte(pluginSource), 0o600); err != nil {
		return nil, fmt.Errorf(
			"opencode adapter: cannot install control-plane hook (guard plugin write failed); refusing to spawn: %w",
			err,
		)
	}
	content, err := marshalGuardConfig()
	if err != nil {
		return nil, fmt.Errorf(
			"opencode adapter: cannot install control-plane hook (config marshal failed); refusing to spawn: %w",
			err,
		)
	}
	return map[string]string{
		"OPENCODE_CONFIG_DIR":     configDir,
		"OPENCODE_CONFIG_CONTENT": string(content),
		"SPRINGFIELD_HOOK_BIN":    a.hookBin,
	}, nil
}
