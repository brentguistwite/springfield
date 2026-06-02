package skills

import (
	"fmt"
	"strings"

	"springfield/internal/features/playbooks"
)

// Name identifies one Springfield-owned skill contract.
type Name string

const (
	SkillPlan    Name = "plan"
	SkillJira    Name = "jira"
	SkillStatus  Name = "status"
	SkillRecover Name = "recover"
)

// MinCLIVersion is the generous, hand-maintained floor: the oldest springfield
// CLI these skills are known to work against. Bump it manually — and rarely —
// only when a skill starts depending on a newer CLI capability. It is NOT
// auto-synced to version.txt; cmd/regen does not touch it. The asymmetry is
// intentional: the plugin updates lag the CLI (manual on both Claude Code and
// Codex), so "older plugin + newer CLI" is the normal steady state and must
// stay silent. The floor only fires for the rarer "plugin needs a newer CLI"
// direction, where the remediation is `brew upgrade springfield`.
const MinCLIVersion = "0.11.0"

// versionCheckPreamble is prepended to every skill body so the agent verifies
// the CLI is present and recent enough before doing any Springfield work, and
// surfaces an actionable install/upgrade command instead of a cryptic failure.
var versionCheckPreamble = strings.TrimSpace(`
## Before you start — verify the Springfield CLI

Run ` + "`springfield version`" + ` first. It prints one of:
- ` + "`springfield vX.Y.Z`" + ` — a released build.
- ` + "`springfield dev`" + ` — a local source build (e.g. ` + "`go install .`" + `).

Then:

- If the command is **not found**, the CLI is not installed. Tell the user to install it, then stop:
  - macOS: ` + "`brew install brentguistwite/tap/springfield`" + `
  - Linux: download the matching ` + "`springfield_<version>_linux_<arch>.tar.gz`" + ` from the GitHub Releases page and put the ` + "`springfield`" + ` binary on PATH.
  - Windows: build from source with ` + "`go install .`" + ` inside the Springfield repo (no Windows release tarballs are published yet).
- If the output is ` + "`springfield dev`" + `, this is a local development build. Continue without a floor check — the user is responsible for keeping it current.
- If the reported version is **older than v` + MinCLIVersion + `** (compare semver-style after stripping the leading ` + "`v`" + ` from the reported version), tell the user to upgrade, then stop:
  - macOS: ` + "`brew upgrade springfield`" + `
  - Linux: download the latest ` + "`springfield_<version>_linux_<arch>.tar.gz`" + ` from the GitHub Releases page and replace the binary on PATH.
  - Windows: ` + "`go install .`" + ` inside the Springfield repo (no Windows release tarballs yet).
- Otherwise continue.

Do not try to work around a missing or too-old CLI — surface the exact command above instead. (A plugin older than the CLI is fine and needs no action; the CLI stays backward-compatible with older skills within a major version.)

## Springfield control plane

**Reads are allowed** — recover and status flows specifically inspect ` + "`.springfield/run.json`" + ` and per-plan ` + "`prd.json`" + `. **Never write, edit, or delete** files under ` + "`.springfield/`" + `. That directory is Springfield's state — the CLI is your only interface for mutating it. Writing there directly will abort the current batch. This applies regardless of which agent is invoking the skill.

---
`)

// Skill describes one canonical Springfield skill file.
type Skill struct {
	Name         Name
	Summary      string
	Purpose      playbooks.Purpose
	Header       string
	Description  string
	RelativePath string
	TaskBody     string
}

// LocalTarget describes one local sync destination used by the helper CLI.
type LocalTarget struct {
	Name         string
	Summary      string
	Purpose      playbooks.Purpose
	TaskBody     string
	Header       string
	Description  string
	RelativePath string
}

// Rendered is the resolved prompt plus checked-in skill file content.
type Rendered struct {
	Skill   Skill
	Prompt  string
	Content string
}

// Installed describes one written local host artifact file.
type Installed struct {
	Host LocalTarget
	Path string
}

// InstallOptions controls where Springfield writes local host artifacts.
type InstallOptions struct {
	Hosts     []string
	ClaudeDir string
	CodexDir  string
}

var skillCatalog = []Skill{
	{
		Name:         SkillPlan,
		Summary:      "Compile a Springfield plan into a runnable batch.",
		Purpose:      playbooks.PurposePlan,
		Header:       "Springfield Plan",
		Description:  "Use Springfield plan to compile a new work request into a runnable batch for the current project.",
		RelativePath: "skills/plan/SKILL.md",
		TaskBody: versionCheckPreamble + "\n\n" + strings.TrimSpace(`
Compile a Springfield batch from the user's work request.

Read project guidance from AGENTS.md first, then CLAUDE.md, then GEMINI.md when present.

## Step 1 — Determine source

Ask the user whether they have an existing plan file or want to describe the work directly:

1. **Existing plan file**: ask for the file path, then read it.
2. **Fresh prompt**: ask the user to describe what they want to build.

Do not infer file-vs-prompt from one ambiguous input.

## Step 2 — Check for active batch

Run `+"`springfield status`"+` to check whether an active batch already exists.

- If an active batch exists and any slice is `+"`running`"+`, tell the user to wait before replacing.
- If an active batch exists but nothing is running, ask the user: replace it, append to it, or keep it.

## Step 3 — Read and slice the plan

If the user pointed to a file, read it. If prompt-mode, treat the prompt as source.

Decide slice boundaries based on the plan's meaning, not syntax. A slice should:

- Be independently deliverable in one agent run.
- Map to one coherent outcome (e.g., "scaffold auth package", "wire login endpoint").
- Not span unrelated subsystems.

Markdown cues to consider (in priority order):

1. Explicit `+"`## Task N:`"+` / `+"`## Step N:`"+` headers — honor them.
2. H2/H3 sections that each describe a discrete deliverable.
3. Numbered lists of implementation steps.
4. Prose plans — chunk by responsibility.

If the plan is genuinely one step, emit one slice. Don't pad.

## Step 4 — Definition of Done (per slice)

For each slice, settle how we will know it is done — its `+"`acceptance_criteria`"+`.

- **Criteria already present** (the source plan already lists them): show them back read-only and ask the user "edit any?". Do not silently re-draft what they wrote.
- **Thin or missing**: before drafting anything, ask one focused question per slice — "how do we know this is done — what command or observable proves it?" Then draft concrete checks and show them in one compact block for confirmation.

Aim each criterion at something checkable: a test command (e.g. `+"`go test ./auth passes`"+`), a file path, or an HTTP response (e.g. `+"`GET /health returns 200`"+`). Springfield emits a non-fatal warning at ingest on any criterion with no such signal — vague criteria still compile, they just get a nudge.

How criteria are actually used — be honest with the user, don't oversell:

- They sharpen the agent and reviewer prompts. They are NOT a deterministic done-gate.
- The runner re-runs a plan until the agent self-emits `+"`<story-pass>US-NNN</story-pass>`"+` or it hits the iteration cap (default 50). That marker — the agent's own judgment — is what gates completion, not the criteria themselves.
- The optional pre-merge review (next step) is the only independent check that the work actually meets the criteria.

## Step 5 — Offer pre-merge review

Ask, as its own question: **"Enable independent pre-merge review for this batch?"**

- The default lever is per-plan: set `+"`plans[].review`"+` in the envelope to `+"`true`"+` (force review on) or `+"`false`"+` (force it off). Leave it unset to inherit the project default.
- The project-wide default lives in `+"`springfield.local.toml`"+` (`+"`[review].enabled`"+`, an operator-wide concern) — mention it only if the user wants every batch reviewed rather than choosing per-plan.

**Serialize the answer.** Write the **same** `+"`review`"+` value onto *every* plan object — all `+"`true`"+` if the user enabled review, all `+"`false`"+` if they declined (the example below shows `+"`true`"+` on each plan). Only use different values per plan if the user explicitly asks for per-plan control. Omitting `+"`review`"+` means "inherit the project default", so dropping it after the user opted in would silently ship an unreviewed batch.

## Step 6 — Confirm and persist

Show the user the proposed plans (title + one-line intent per plan).
Ask for confirmation before writing.

Once confirmed, compile a **PRD envelope** and pipe it to `+"`springfield plan --prd -`"+`:

`+"```"+`bash
springfield plan --prd - <<'JSON'
{
  "title": "<batch title>",
  "source": "<original plan text, verbatim>",
  "phases": [
    {"mode": "serial", "plans": ["plan-01", "plan-02"]}
  ],
  "plans": [
    {
      "id": "plan-01",
      "title": "<plan 1 title>",
      "description": "<plan 1 description>",
      "context_md": "Project uses TypeScript + Bun. Follow existing test patterns.",
      "review": true,
      "user_stories": [
        {
          "id": "US-001",
          "title": "<story title>",
          "description": "<story description>",
          "acceptance_criteria": ["<criterion 1>"],
          "priority": 1,
          "passes": false,
          "deps": []
        }
      ]
    },
    {
      "id": "plan-02",
      "title": "<plan 2 title>",
      "description": "<plan 2 description>",
      "context_md": "Project uses TypeScript + Bun. Follow existing test patterns.",
      "review": true,
      "user_stories": [
        {
          "id": "US-001",
          "title": "<story title>",
          "description": "<story description>",
          "acceptance_criteria": ["<criterion 1>"],
          "priority": 1,
          "passes": false,
          "deps": []
        }
      ]
    }
  ]
}
JSON
`+"```"+`

Schema notes:
- `+"`phases`"+`: execution ordering. Each phase has `+"`mode`"+` (`+"`\"serial\"`"+` or `+"`\"parallel\"`"+`) and `+"`plans`"+` (list of plan IDs in that phase).
- `+"`plans`"+`: each plan has `+"`id`"+`, `+"`title`"+`, `+"`description`"+`, optional `+"`context_md`"+`, optional `+"`review`"+` (the per-plan pre-merge review toggle from Step 5 — omit to inherit the project default), and `+"`user_stories`"+`.
- `+"`context_md`"+`: plan-specific context only. Project-wide guidance (build commands, repo conventions) lives in root `+"`AGENTS.md`"+` and is auto-loaded by the runner — do not duplicate it into `+"`context_md`"+` or you double the prompt-token cost of that material every iteration.
- `+"`user_stories`"+`: each story has `+"`id`"+` (`+"`US-NNN`"+`), `+"`title`"+`, `+"`description`"+`, `+"`acceptance_criteria`"+`, `+"`priority`"+` (int, lower = runs first), `+"`passes`"+` (false initially), `+"`deps`"+` (story IDs within same plan).
- See `+"`docs/prd-format.md`"+` for full field semantics, validation rules, and stop conditions.

> **Constraint — documentation acceptance criteria must name an explicit target file.**
> Any `+"`acceptance_criteria`"+` entry that prescribes writing or updating documentation MUST name the exact target as a `+"`path/to/file.md`"+` (add a `+"`:line`"+` anchor when pointing at an existing section). If no canonical file exists yet, the criterion must say to create it at a named path.
> Rationale: in a dogfood batch an agent burned ~75 turns hunting for a "review docs" file that never existed because the criterion never named one — vague targets cause thrash.
> - Allowed: `+"`\"Document the off-target marker rule in docs/prd-format.md under the 'Stop Conditions' section\"`"+`
> - Forbidden: `+"`\"Document the off-target marker rule in the review docs\"`"+` and `+"`\"note this in the relevant section\"`"+` — no file path, so the agent has nothing concrete to target.

Use `+"`--replace`"+` or `+"`--append`"+` if an active batch exists (per Step 2).

Keep Springfield as the only user-facing surface.
`),
	},
	{
		Name:         SkillJira,
		Summary:      "Compile a Springfield batch from Jira tickets or an epic.",
		Purpose:      playbooks.PurposePlan,
		Header:       "Springfield Jira",
		Description:  "Use Springfield jira to compile one or more Jira tickets, or an epic and its children, into a runnable batch.",
		RelativePath: "skills/jira/SKILL.md",
		TaskBody: versionCheckPreamble + "\n\n" + strings.TrimSpace(`
Compile a Springfield batch from one or more Jira tickets, or an epic and its child tickets.

Read project guidance from AGENTS.md first, then CLAUDE.md, then GEMINI.md when present.

This skill emits the SAME PRD envelope as the `+"`plan`"+` skill (see `+"`docs/prd-format.md`"+`) and pipes it to `+"`springfield plan --prd -`"+`. Its only added surface is reaching Jira and mapping tickets onto the envelope — the Definition-of-Done and pre-merge-review steps are identical to the `+"`plan`"+` skill.

## Step 1 — Confirm a Jira tool is available (precondition, not setup)

Springfield does NOT manage Jira access. Detect whatever the running agent already has, in this order:

1. An Atlassian MCP server (tools named like `+"`getJiraIssue`"+`, `+"`searchJiraIssuesUsingJql`"+`).
2. A `+"`jira`"+` CLI on PATH (`+"`jira issue view <KEY>`"+`).
3. Jira REST credentials (a base URL + API token the agent can curl).

Use the first one found. If none is available, stop and tell the user: "No Jira tool detected. Connect the Atlassian MCP, install the `+"`jira`"+` CLI, or set Jira REST credentials, then re-run." Do not attempt to install or authenticate anything.

## Step 2 — Determine input

Ask the user once: "Which tickets? List ticket keys (e.g. PROJ-123), paste ticket URLs, or give me an epic key."

- Parse the key out of any `+"`.../browse/PROJ-123`"+` URL.
- If unsure whether a key is an epic, fetch it and check its issue type — do not guess epic-vs-ticket from one ambiguous input.

## Step 3 — Resolve scope (epic expansion)

For bare ticket keys, fetch each directly.

For an epic key, query its children with the detected tool. Field names vary by Jira instance — try `+"`parent = <EPIC>`"+` first, then `+"`\"Epic Link\" = <EPIC>`"+` if the first returns nothing.

**Guardrail:** after resolving children, show the discovered list (key + title, in resolved order) and ask "These N tickets — right?" before the heavier per-ticket fetch. This catches wrong-field / wrong-epic results before they become a batch.

## Step 4 — Check for active batch

Run `+"`springfield status`"+` to check whether an active batch already exists.

- **No active batch** → proceed with a fresh compile (the normal path).
- **A batch is currently running** (any plan `+"`running`"+`) → adding Jira tickets to a live run is not supported. Tell the user to let it finish (or stop it with `+"`springfield recover`"+`) and re-run jira afterward. Do not modify a running batch.
- **A batch exists but has not started** (nothing running) → you may fold the new tickets in, but **rebuild the combined set**, do not append. `+"`springfield status`"+` lists the plans already queued (their ids are the slugged ticket keys, e.g. `+"`proj-123`"+`); show them to the user and ask for the additional ticket keys. Re-fetch that full set — the already-queued tickets plus the new ones — into ONE envelope and write it with `+"`--replace`"+`. Because nothing has run, replace loses nothing, and the topological sort covers old + new together, so a new ticket that blocks an already-queued one still orders correctly.

Do **not** use `+"`--append`"+` for jira ingest: it adds phases *after* the existing batch (so it cannot reorder across the boundary — an appended blocker would run after its dependent) and drops the appended tickets' `+"`source`"+` audit. Rebuilding the combined set with `+"`--replace`"+` is always correct here precisely because the batch has not started.

## Step 5 — Map tickets to the envelope

Grain:

- The epic (or the set of standalone tickets) → the **batch**.
- Each child / standalone ticket → one **plan**. Slug the plan `+"`id`"+` from the ticket key: lowercase it and replace any character outside `+"`[a-z0-9-]`"+` with a hyphen, collapsing runs (`+"`PROJ-123`"+` → `+"`proj-123`"+`; `+"`MY_PROJECT-45`"+` → `+"`my-project-45`"+`). The envelope requires `+"`^[a-z0-9][a-z0-9-]*$`"+`. This keeps the envelope, `+"`.springfield/plans/<id>/`"+`, and the Jira ticket aligned by key.
- A ticket's **subtasks → user stories** in that plan. A ticket with no subtasks → one synthetic user story covering the ticket itself.
- **Don't lose the parent ticket's own Definition of Done.** Only `+"`user_stories[].acceptance_criteria`"+` is consumed by the executor and the review gate — `+"`context_md`"+` is context, never a done-gate. So when a ticket has subtasks AND its own acceptance criteria, the subtasks become the implementation stories AND you append one final synthetic **parent-acceptance** story whose `+"`acceptance_criteria`"+` are the parent ticket's, with `+"`deps`"+` on every subtask story and a `+"`priority`"+` higher than all of them (so it runs last; `+"`priority`"+` must be `+"`>= 1`"+`, never the `+"`0`"+` zero-value). Never strip the parent's criteria into `+"`context_md`"+` oblivion when subtasks exist — they must ride in a story the runner actually checks.
- Story IDs are `+"`US-001`"+`, `+"`US-002`"+`, … per plan (the envelope requires `+"`^US-\\d{3,}$`"+`; the ticket key lives in context, not the story id).

Ordering and dependencies (honor the structure Jira already holds):

- **Plan order** within the single serial phase: topologically sort tickets by `+"`blocks`"+` / `+"`is blocked by`"+` links; where there is no link, fall back to Jira backlog rank, then priority, then the order the user listed them.
- **Story deps** (`+"`user_stories[].deps`"+`): a `+"`blocks`"+` link between two subtasks of the **same** parent ticket → a dep within that plan. **Skip** any `+"`blocks`"+` link whose target subtask belongs to a different parent ticket — the envelope forbids cross-plan deps, so a cross-ticket link is a plan-order edge at most, never a story dep (emitting it produces a `+"`dep not found in same plan`"+` hard error).
- A dependency cycle → degrade, never fail. At the **plan** level (`+"`A blocks B blocks A`"+`) drop the cyclic ordering edge and keep rank order. At the **story** level (two subtasks blocking each other) drop **both** `+"`deps`"+` links and emit the stories in rank order — a cyclic `+"`deps`"+` passes envelope validation but leaves no eligible story, so the runner hard-fails the plan with `+"`story dependency graph blocked: no eligible story`"+`. Note the degradation either way.

Text sinks:

- `+"`plans[].context_md`"+`: the ticket description prose with the acceptance-criteria section removed (those become `+"`acceptance_criteria`"+`), prefixed with a one-line `+"`Source: PROJ-123 — <title>`"+` plus the ticket URL. Do NOT include comments. Do NOT duplicate project-wide guidance (root AGENTS.md is auto-loaded by the runner). Keep it bounded: `+"`context_md`"+` warns past ~32 KB and hard-errors past 256 KB, so for a long ticket — pasted logs, stack traces, incident dumps — summarize or clip the description to the decision-relevant prose and leave the full raw text to `+"`source`"+` (below).
- `+"`source`"+` (batch-level, stored as `+"`source.md`"+` for audit): the concatenated raw fetched tickets (key, title, description, acceptance criteria).

## Step 6 — Definition of Done (per story)

Acceptance criteria are PROMPT INPUT, not a deterministic done-gate — the same contract as the `+"`plan`"+` skill. Extract them per story, in order:

1. A dedicated acceptance-criteria custom field on the ticket/subtask.
2. An `+"`Acceptance Criteria`"+` heading or checklist in the description.
3. None found.

Then, per story:

- **Criteria found** → show them back read-only and ask "edit any?". Do not silently re-draft what the team wrote in Jira.
- **None found** → ask one focused question: "how do we know this story is done — what command or observable proves it?" Then draft concrete, checkable criteria and show them for confirmation.

Aim each criterion at something checkable: a test command (e.g. `+"`go test ./auth passes`"+`), a file path, or an HTTP response (e.g. `+"`GET /health returns 200`"+`). Springfield emits a non-fatal warning at ingest on any criterion with no such signal — vague criteria still compile, they just get a nudge.

**Bulk escape hatch:** if the user asks to ingest without per-story questions (e.g. "ingest the whole epic, don't ask me about criteria"), skip the interactive prompts: where extraction yields at least one criterion, take it as-is and let the ingest `+"`[warn]`"+` flag weak phrasing. But an **empty** `+"`acceptance_criteria`"+` list is a HARD validation error that aborts ingest, not a warning — so for a story where extraction found nothing, still emit one minimal checkable criterion drawn from the ticket itself (its title/description) rather than an empty list. Do not invent elaborate criteria the team did not write.

How criteria are actually used — be honest, don't oversell:

- They sharpen the agent and reviewer prompts. They are NOT a deterministic done-gate.
- The runner re-runs a plan until the agent self-emits `+"`<story-pass>US-NNN</story-pass>`"+` or it hits the iteration cap (default 50). That marker — the agent's own judgment — gates completion, not the criteria.
- The optional pre-merge review (next step) is the only independent check that the work meets the criteria.

## Step 7 — Offer pre-merge review

Ask, as its own question: **"Enable independent pre-merge review for this batch?"**

- The default lever is per-plan: set `+"`plans[].review`"+` in the envelope to `+"`true`"+` (force review on) or `+"`false`"+` (force it off). Leave it unset to inherit the project default.
- The project-wide default lives in `+"`springfield.local.toml`"+` (`+"`[review].enabled`"+`, an operator-wide concern) — mention it only if the user wants every batch reviewed rather than choosing per-plan.

**Serialize the answer.** Write the **same** `+"`review`"+` value onto *every* plan object — all `+"`true`"+` if the user enabled review, all `+"`false`"+` if they declined. Only use different values per plan if the user explicitly asks for per-plan control. Omitting `+"`review`"+` means "inherit the project default", so dropping it after the user opted in would silently ship an unreviewed batch.

## Step 8 — Confirm and persist

Show the user the proposed plans (one line per plan: `+"`proj-123 — <title>`"+`).
Ask for confirmation before writing.

Once confirmed, compile a **PRD envelope** and pipe it to `+"`springfield plan --prd -`"+`:

`+"```"+`bash
springfield plan --prd - <<'JSON'
{
  "title": "<epic or batch title>",
  "source": "<concatenated raw tickets, verbatim>",
  "phases": [
    {"mode": "serial", "plans": ["proj-123", "proj-124"]}
  ],
  "plans": [
    {
      "id": "proj-123",
      "title": "<ticket 123 summary>",
      "description": "<ticket 123 one-paragraph summary>",
      "context_md": "Source: PROJ-123 — <title>\nhttps://your-domain.atlassian.net/browse/PROJ-123\n\n<ticket description minus the acceptance-criteria section>",
      "review": true,
      "user_stories": [
        {
          "id": "US-001",
          "title": "<subtask 1 summary>",
          "description": "<subtask 1 description>",
          "acceptance_criteria": ["<criterion from subtask 1>"],
          "priority": 1,
          "passes": false,
          "deps": []
        },
        {
          "id": "US-002",
          "title": "<subtask 2 summary>",
          "description": "<subtask 2 description>",
          "acceptance_criteria": ["<criterion from subtask 2>"],
          "priority": 2,
          "passes": false,
          "deps": ["US-001"]
        }
      ]
    },
    {
      "id": "proj-124",
      "title": "<ticket 124 summary>",
      "description": "<ticket 124 one-paragraph summary>",
      "context_md": "Source: PROJ-124 — <title>\nhttps://your-domain.atlassian.net/browse/PROJ-124\n\n<ticket description minus the acceptance-criteria section>",
      "review": true,
      "user_stories": [
        {
          "id": "US-001",
          "title": "<synthetic story for a subtask-less ticket>",
          "description": "<ticket 124 description>",
          "acceptance_criteria": ["<criterion>"],
          "priority": 1,
          "passes": false,
          "deps": []
        }
      ]
    }
  ]
}
JSON
`+"```"+`

Schema notes:
- `+"`phases`"+`: execution ordering. Each phase has `+"`mode`"+` (`+"`\"serial\"`"+`) and `+"`plans`"+` (list of plan IDs in that phase, in run order).
- `+"`plans`"+`: each plan has `+"`id`"+` (slugged from the ticket key, lowercased), `+"`title`"+`, `+"`description`"+`, optional `+"`context_md`"+`, optional `+"`review`"+` (the per-plan toggle from Step 7 — omit to inherit the project default), and `+"`user_stories`"+`.
- `+"`context_md`"+`: plan-specific context only (the ticket's own description + a `+"`Source:`"+` header). Project-wide guidance lives in root `+"`AGENTS.md`"+` and is auto-loaded — do not duplicate it.
- `+"`user_stories`"+`: each story has `+"`id`"+` (`+"`US-NNN`"+`, NOT the ticket key), `+"`title`"+`, `+"`description`"+`, `+"`acceptance_criteria`"+`, `+"`priority`"+` (int, lower = runs first), `+"`passes`"+` (false initially), `+"`deps`"+` (story IDs within the same plan, from subtask blocks links).
- See `+"`docs/prd-format.md`"+` for full field semantics, validation rules, and stop conditions.

> **Constraint — documentation acceptance criteria must name an explicit target file.**
> Any `+"`acceptance_criteria`"+` entry that prescribes writing or updating documentation MUST name the exact target as a `+"`path/to/file.md`"+` (add a `+"`:line`"+` anchor when pointing at an existing section). If no canonical file exists yet, the criterion must say to create it at a named path.
> Rationale: in a dogfood batch an agent burned ~75 turns hunting for a "review docs" file that never existed because the criterion never named one — vague targets cause thrash.
> - Allowed: `+"`\"Document the off-target marker rule in docs/prd-format.md under the 'Stop Conditions' section\"`"+`
> - Forbidden: `+"`\"Document the off-target marker rule in the review docs\"`"+` and `+"`\"note this in the relevant section\"`"+` — no file path, so the agent has nothing concrete to target.

If an unstarted batch already exists, write the **combined** set (existing + new tickets) with `+"`--replace`"+` (per Step 4). Do not use `+"`--append`"+` for jira ingest. If a batch is running, stop — adding to a live run is not supported.

## Out of scope

This skill reads Jira and emits a batch. It does NOT write back to Jira — no status transitions, no "batch started" comments. Closing that loop is the operator's job.

Keep Springfield as the only user-facing surface.
`),
	},
	{
		Name:         SkillStatus,
		Summary:      "Inspect the active Springfield batch and explain where it stands.",
		Purpose:      playbooks.PurposeStatus,
		Header:       "Springfield Status",
		Description:  "Use Springfield status to inspect the active batch and explain where it stands.",
		RelativePath: "skills/status/SKILL.md",
		TaskBody: versionCheckPreamble + "\n\n" + strings.TrimSpace(`
Inspect the current Springfield batch for the project and report the current state.

Read project guidance from AGENTS.md first, then CLAUDE.md, then GEMINI.md when present.

Run `+"`springfield status`"+` to get the current Springfield batch state, then summarize:
- The active batch id and title
- The current phase
- Which slices are done, running, blocked, or queued
- Per-plan story progress: e.g. "Plan 03: 5/8 stories pass; failed at US-006"
- The last known error if any
- The clearest next action for the user

Do not invent new work unless the user explicitly asks to re-plan it.
Keep Springfield as the only user-facing surface.
`),
	},
	{
		Name:         SkillRecover,
		Summary:      "Diagnose a stuck batch or failed slice and restore a safe next step.",
		Purpose:      playbooks.PurposeRecover,
		Header:       "Springfield Recover",
		Description:  "Use Springfield recover to diagnose a stuck batch or failed slice and restore a safe next step.",
		RelativePath: "skills/recover/SKILL.md",
		TaskBody: versionCheckPreamble + "\n\n" + strings.TrimSpace(`
Recover a Springfield batch that is stalled, blocked, or has a failed slice.

Read project guidance from AGENTS.md first, then CLAUDE.md, then GEMINI.md when present.

## Step 1 — Read current state

Run `+"`springfield status`"+` to see the active batch, current phase, and slice statuses.

For a failing or stalled plan, run `+"`springfield recover --diagnose --plan <plan-id>`"+` first. The CLI surfaces dirty-worktree state, `+"`exit_reason`"+`, orphan-plan handling, and the available recovery actions — interpret that output rather than re-deriving it from raw files.

Also read `+"`.springfield/run.json`"+` for the last checkpoint and last known error.

## Step 2 — Diagnose

Identify which plan and which story failed or stalled. Check:
- The CLI diagnose output (above) — primary source.
- The last error in `+"`run.json`"+`
- Per-story `+"`passes`"+` state in each plan's `+"`prd.json`"+` (`+"`.springfield/plans/<plan-id>/prd.json`"+`)
- Any blockers mentioned in the batch source

## Step 3 — Recover

Propose the safest concrete next step:
- For a failed plan: fix the underlying issue, then run `+"`springfield start`"+` to resume from cursor. Already-passed stories (`+"`passes: true`"+`) are skipped on re-entry — `+"`prd.json`"+` is preserved across retries.
- For a blocked plan: explain what needs to happen before execution can continue.
- For a corrupt batch: use `+"`springfield plan --replace`"+` to start fresh with a new batch.

Story-level retry is not supported in v1; recovery operates at plan grain (retry plan or retry merge).

Prefer recovery and continuation over starting a fresh plan unless the existing state cannot be salvaged.
Keep Springfield as the only user-facing surface.
`),
	},
}

var localTargets = []LocalTarget{
	{
		Name:         "claude-code",
		Summary:      "Springfield local helper command for Claude Code.",
		Purpose:      playbooks.PurposePlan,
		Header:       "Springfield Command",
		Description:  "Use `/springfield` in Claude Code to run Springfield inside this project.",
		RelativePath: "springfield.md",
		TaskBody: strings.TrimSpace(`
Use Springfield as the primary agent-native surface for this project.

Keep Springfield as the only user-facing surface.
If the user asks what Springfield does, explain the current project context and Springfield guidance before planning or execution.
When work is requested, ask clarifying questions when needed, then drive toward a concrete Springfield work definition with named workstreams.
Stay aligned with the shared Springfield playbook and the current project's guidance.

Control plane: reads are allowed for diagnosis; never write, edit, or delete files under the .springfield/ directory. The Springfield CLI is the only interface for mutating that state.
`),
	},
	{
		Name:         "codex",
		Summary:      "Springfield local helper skill for Codex.",
		Purpose:      playbooks.PurposePlan,
		Header:       "Springfield Skill",
		Description:  "Use the Springfield skill in Codex to run Springfield inside this project.",
		RelativePath: "springfield/SKILL.md",
		TaskBody: strings.TrimSpace(`
Use Springfield as the primary agent-native surface for this project.

Keep Springfield as the only user-facing surface.
If the user asks what Springfield does, explain the current project context and Springfield guidance before planning or execution.
When work is requested, ask clarifying questions when needed, then drive toward a concrete Springfield work definition with named workstreams.
Stay aligned with the shared Springfield playbook and the current project's guidance.

Control plane: reads are allowed for diagnosis; never write, edit, or delete files under the .springfield/ directory. The Springfield CLI is the only interface for mutating that state.
`),
	},
}

// Catalog returns the canonical Springfield skill catalog.
func Catalog() []Skill {
	out := make([]Skill, len(skillCatalog))
	copy(out, skillCatalog)
	return out
}

// Lookup resolves one canonical Springfield skill.
func Lookup(name string) (Skill, error) {
	for _, skill := range skillCatalog {
		if string(skill.Name) == name {
			return skill, nil
		}
	}
	return Skill{}, fmt.Errorf("unknown Springfield skill %q", name)
}

func localCatalog() []LocalTarget {
	out := make([]LocalTarget, len(localTargets))
	copy(out, localTargets)
	return out
}
