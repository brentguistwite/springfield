---
description: Use Springfield plan-from-issue to compile one or more tracker issues, or a container (Jira epic / Linear project) and its children, into a runnable batch.
---

# Springfield Plan from Issue

Use Springfield plan-from-issue to compile one or more tracker issues, or a container (Jira epic / Linear project) and its children, into a runnable batch.

# Springfield Playbook
Source: builtin/springfield.md

# Springfield

Built-in Springfield playbook.

- Keep Springfield as the only user-facing surface.
- Use the shared Springfield playbook guidance to shape planning and explanation.
- Keep internal engine details out of Springfield-owned prompt surfaces.

# Current Task

## Before you start — verify the Springfield CLI

Run `springfield version` first. It prints one of:
- `springfield vX.Y.Z` — a released build.
- `springfield dev` — a local source build (e.g. `go install .`).

Then:

- If the command is **not found**, the CLI is not installed. Tell the user to install it, then stop:
  - macOS: `brew install brentguistwite/tap/springfield`
  - Linux: download the matching `springfield_<version>_linux_<arch>.tar.gz` from the GitHub Releases page and put the `springfield` binary on PATH.
  - Windows: build from source with `go install .` inside the Springfield repo (no Windows release tarballs are published yet).
- If the output is `springfield dev`, this is a local development build. Continue without a floor check — the user is responsible for keeping it current.
- If the reported version is **older than v0.11.0** (compare semver-style after stripping the leading `v` from the reported version), tell the user to upgrade, then stop:
  - macOS: `brew upgrade springfield`
  - Linux: download the latest `springfield_<version>_linux_<arch>.tar.gz` from the GitHub Releases page and replace the binary on PATH.
  - Windows: `go install .` inside the Springfield repo (no Windows release tarballs yet).
- Otherwise continue.

Do not try to work around a missing or too-old CLI — surface the exact command above instead. (A plugin older than the CLI is fine and needs no action; the CLI stays backward-compatible with older skills within a major version.)

## Springfield control plane

**Reads are allowed** — recover and status flows specifically inspect `.springfield/run.json` and per-plan `prd.json`. **Never write, edit, or delete** files under `.springfield/`. That directory is Springfield's state — the CLI is your only interface for mutating it. Writing there directly will abort the current batch. This applies regardless of which agent is invoking the skill.

---

Compile a Springfield batch from one or more tracker issues, or a container (Jira epic / Linear project) and its child issues.

Read project guidance from AGENTS.md first, then CLAUDE.md, then GEMINI.md when present.

This skill emits the SAME PRD envelope as the `plan` skill (see `docs/prd-format.md`) and pipes it to `springfield plan --prd -`. Its only added surface is reaching the active tracker and mapping issues onto the envelope — the Definition-of-Done and pre-merge-review steps are identical to the `plan` skill. Everything except the per-tracker mechanics is tracker-neutral; the tracker-specific fetch, key parsing, container expansion, and field mapping live in the **Tracker profile** blocks at the bottom.

## Step 0 — Select the active tracker

Decide which **Tracker profile** (below) governs this run:

1. If `springfield.toml` has a `tracker = "<name>"` line, use that profile. (Read the toml directly — no engine config parsing.) **Validate** the value against the `### Tracker profile:` headings below; on a miss, stop and surface: `unknown tracker "<value>" — known profiles: jira, linear; fix springfield.toml or tell me which`. The `### Tracker profile:` headings are the registry — there is no tracker-name list in code, so keep this known-profiles hint in sync with the headings if you add a profile.
2. Otherwise auto-detect by the tool the agent already has connected, using these inline signals: a Jira tool (e.g. `getJiraIssue` / `searchJiraIssuesUsingJql`) → **jira**; a Linear tool (e.g. `get_issue` via `mcp.linear.app`) → **linear**.
3. If both or neither are connected, ask once: "Which tracker — jira or linear?"

Then follow the matching **Tracker profile** block below for fetch, key parsing, container expansion, and field mapping. Everything else in this skill is tracker-neutral.

## Step 1 — Confirm an issue-tracker tool is available (precondition, not setup)

Springfield does NOT manage tracker access. This is a precondition, not setup — detect whatever the running agent already has per the active profile's **Fetch** list, using the first one found. If none is available, stop and surface the full "No issue-tracker tool detected for <tracker>…" message spelled out at the end of the active profile's **Fetch** bullet (it already names the connect options). Do not attempt to install or authenticate anything.

## Step 2 — Determine input

Ask the user once: "Which issues? List issue keys, paste issue URLs, or give me a container key (the active profile's container — a Jira epic or a Linear project)."

- Parse the key out of any issue URL per the active profile's **Issue reference** rule.
- If unsure whether a key is a container, fetch it and check — do not guess container-vs-issue from one ambiguous input.

## Step 3 — Resolve scope (container expansion)

For bare issue keys, fetch each directly.

For a container key, expand it to its child issues per the active profile's **Container expansion** rule.

**Guardrail:** after resolving children, show the discovered list (key + title, in resolved order) and ask "These N issues — right?" before the heavier per-issue fetch. This catches wrong-container / wrong-field results before they become a batch.

## Step 4 — Check for active batch

Run `springfield status` to check whether an active batch already exists.

- **No active batch** → proceed with a fresh compile (the normal path; persist with plain `springfield plan --prd -`).
- **An active batch exists** → you cannot decide on your own whether it is safe to replace. `springfield status` **cannot prove a batch never started** — several already-started states (a signal-interrupted plan, or a plan that ran but stalled at merge or cleanup) render the same `0/N` queued shape as a never-run batch, and `springfield plan --replace` archives a started batch's work and evidence without complaint (its only guard is a *currently-running* plan). No programmatic signal can prove it, so the **user** is the only gate — and the question is the batch's whole history on this checkout (any session or operator), never just whether the current user personally ran it:
  - Show the existing batch's queued plan ids from `springfield status` and ask about the batch's **whole history, not the user's own actions** (`.springfield` state is durable across sessions, so a prior agent run or another operator on this checkout could have started it): **"Was this batch created in this session and never started by ANY session — you, another operator, or a prior agent run?"**
  - **User positively confirms it was created this session and never started by anyone** → safe to discard. Ask the user for the COMPLETE issue-key set for the rebuilt batch (the already-queued issues plus the new ones). Do **not** try to reverse the queued plan ids back into issue keys — that slug is lossy (`MY_PROJECT-45` and `MY-PROJECT-45` both slug to `my-project-45`) and the batch may not even be tracker-derived; the user supplies the keys, and `springfield status` ids are only a reminder of what is there. Build ONE envelope from that full set and write it with `springfield plan --replace --prd -` — the topological sort then covers old + new together, so a new issue that blocks an already-queued one still orders correctly.
  - **Anything else — started, unsure, or unknown provenance** → do NOT replace (default here on any doubt). Batch recovery is the `springfield:recover` skill's job, not this one — point the user there. Do not just tell them to run bare `springfield recover`: that only archives an *orphaned* batch (missing `batch.json`); a failed plan is reset with `springfield recover --plan <id>`, and a running batch must finish or be stopped. Have them run `springfield status`, resolve the batch via `springfield:recover`, then re-run plan-from-issue to compile fresh.

Never use `--append` for plan-from-issue ingest: it adds phases *after* the existing batch (so it cannot reorder across the boundary — an appended blocker would run after its dependent) and drops the appended issues' `source` audit.

## Step 5 — Map issues to the envelope

Grain:

- The container (or the set of standalone issues) → the **batch**.
- Each child / standalone issue → one **plan**. Slug the plan `id` from the issue key: lowercase it and replace any character outside `[a-z0-9-]` with a hyphen, collapsing runs (`PROJ-123` → `proj-123`; `MY_PROJECT-45` → `my-project-45`). The envelope requires `^[a-z0-9][a-z0-9-]*$`. This keeps the envelope, `.springfield/plans/<id>/`, and the tracker issue aligned by key.
- An issue's **sub-items → user stories** in that plan (the sub-item concept named in the active profile — Jira subtasks, Linear sub-issues). An issue with no sub-items → one synthetic user story covering the issue itself.
- **Don't lose the parent issue's own Definition of Done.** Only `user_stories[].acceptance_criteria` is consumed by the executor and the review gate — `context_md` is context, never a done-gate. So when an issue has sub-items AND its own acceptance criteria, the sub-items become the implementation stories AND you append one final synthetic **parent-acceptance** story whose `acceptance_criteria` are the parent issue's, with `deps` on every sub-item story and a `priority` higher than all of them (so it runs last; `priority` must be `>= 1`, never the `0` zero-value). Never strip the parent's criteria into `context_md` oblivion when sub-items exist — they must ride in a story the runner actually checks.
- Story IDs are `US-001`, `US-002`, … per plan (the envelope requires `^US-\d{3,}$`; the issue key lives in context, not the story id).

Ordering and dependencies (honor the structure the active tracker already holds — use the dependency links and ordering signals named in the active profile):

- **Plan order** within the single serial phase: topologically sort issues by the profile's dependency links (`blocks` / `is blocked by`); where there is no link, fall back to the active profile's **ordering signals** (each profile lists them in priority order — don't re-rank), then the order the user listed them.
- **Story deps** (`user_stories[].deps`): a dependency link between two sub-items of the **same** parent issue → a dep within that plan. **Skip** any link whose target sub-item belongs to a different parent issue — the envelope forbids cross-plan deps, so a cross-issue link is a plan-order edge at most, never a story dep (emitting it produces a `dep not found in same plan` hard error).
- A dependency cycle → degrade, never fail. At the **plan** level (`A blocks B blocks A`) drop the cyclic ordering edge and keep rank order. At the **story** level (two sub-items blocking each other) drop **both** `deps` links and emit the stories in rank order — a cyclic `deps` passes envelope validation but leaves no eligible story, so the runner hard-fails the plan with `story dependency graph blocked: no eligible story`. Note the degradation either way.

Text sinks:

- `plans[].context_md`: the issue description prose with the acceptance-criteria section removed (those become `acceptance_criteria`), prefixed with the one-line `Source:` header named in the active profile plus the issue URL. Do NOT include comments. Do NOT duplicate project-wide guidance (root AGENTS.md is auto-loaded by the runner). Keep it bounded: `context_md` warns past ~32 KB and hard-errors past 256 KB, so for a long issue — pasted logs, stack traces, incident dumps — summarize or clip the description to the decision-relevant prose and leave the full raw text to `source` (below).
- `source` (batch-level, stored as `source.md` for audit): the concatenated raw fetched issues (key, title, description, acceptance criteria).

## Step 6 — Definition of Done (per story)

Acceptance criteria are PROMPT INPUT, not a deterministic done-gate — the same contract as the `plan` skill. Extract them per story per the active profile's **Acceptance criteria location**.

Then, per story:

- **Criteria found** → show them back read-only and ask "edit any?". Do not silently re-draft what the team wrote in the tracker.
- **None found** → ask one focused question: "how do we know this story is done — what command or observable proves it?" Then draft concrete, checkable criteria and show them for confirmation.

Aim each criterion at something checkable: a test command (e.g. `go test ./auth passes`), a file path, or an HTTP response (e.g. `GET /health returns 200`). Springfield emits a non-fatal warning at ingest on any criterion with no such signal — vague criteria still compile, they just get a nudge.

**Bulk escape hatch:** if the user asks to ingest without per-story questions (e.g. "ingest the whole container, don't ask me about criteria"), skip the interactive prompts: where extraction yields at least one criterion, take it as-is and let the ingest `[warn]` flag weak phrasing. But an **empty** `acceptance_criteria` list is a HARD validation error that aborts ingest, not a warning — so for a story where extraction found nothing, still emit one minimal checkable criterion drawn from the issue itself (its title/description) rather than an empty list. Do not invent elaborate criteria the team did not write.

How criteria are actually used — be honest, don't oversell:

- They sharpen the agent and reviewer prompts. They are NOT a deterministic done-gate.
- The runner re-runs a plan until the agent self-emits `<story-pass>US-NNN</story-pass>` or it hits the iteration cap (default 50). That marker — the agent's own judgment — gates completion, not the criteria.
- The optional pre-merge review (next step) is the only independent check that the work meets the criteria.

## Step 7 — Offer pre-merge review

Ask, as its own question: **"Enable independent pre-merge review for this batch?"**

- The default lever is per-plan: set `plans[].review` in the envelope to `true` (force review on) or `false` (force it off). Leave it unset to inherit the project default.
- The project-wide default lives in `springfield.local.toml` (`[review].enabled`, an operator-wide concern) — mention it only if the user wants every batch reviewed rather than choosing per-plan.

**Serialize the answer.** Write the **same** `review` value onto *every* plan object — all `true` if the user enabled review, all `false` if they declined. Only use different values per plan if the user explicitly asks for per-plan control. Omitting `review` means "inherit the project default", so dropping it after the user opted in would silently ship an unreviewed batch.

## Step 8 — Confirm and persist

Show the user the proposed plans (one line per plan: `<issue-key> — <title>`).
Ask for confirmation before writing.

Once confirmed, compile a **PRD envelope** and pipe it to the plan CLI. The flag depends on the Step 4 branch: with **no active batch** use plain `springfield plan --prd -` (as shown below); when **rebuilding a batch the user confirmed never started** use `springfield plan --replace --prd -` (plain `--prd -` hard-errors when an active batch exists). Never `--append`.

```bash
springfield plan --prd - <<'JSON'
{
  "title": "<container or batch title>",
  "source": "<concatenated raw issues, verbatim>",
  "phases": [
    {"mode": "serial", "plans": ["proj-123", "b2b2c-299"]}
  ],
  "plans": [
    {
      "id": "proj-123",
      "title": "<issue 1 summary>",
      "description": "<issue 1 one-paragraph summary>",
      "context_md": "Source: PROJ-123 — <title>\n<issue url>\n\n<issue description minus the acceptance-criteria section>",
      "review": true,
      "user_stories": [
        {
          "id": "US-001",
          "title": "<sub-item 1 summary>",
          "description": "<sub-item 1 description>",
          "acceptance_criteria": ["<criterion from sub-item 1>"],
          "priority": 1,
          "passes": false,
          "deps": []
        },
        {
          "id": "US-002",
          "title": "<sub-item 2 summary>",
          "description": "<sub-item 2 description>",
          "acceptance_criteria": ["<criterion from sub-item 2>"],
          "priority": 2,
          "passes": false,
          "deps": ["US-001"]
        }
      ]
    },
    {
      "id": "b2b2c-299",
      "title": "<issue 2 summary>",
      "description": "<issue 2 one-paragraph summary>",
      "context_md": "Source: B2B2C-299 — <title>\n<issue url>\n\n<issue description minus the acceptance-criteria section>",
      "review": true,
      "user_stories": [
        {
          "id": "US-001",
          "title": "<synthetic story for an issue with no sub-items>",
          "description": "<issue 2 description>",
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
```

Schema notes:
- `phases`: execution ordering. Each phase has `mode` (`"serial"`) and `plans` (list of plan IDs in that phase, in run order).
- `plans`: each plan has `id` (slugged from the issue key, lowercased), `title`, `description`, optional `context_md`, optional `review` (the per-plan toggle from Step 7 — omit to inherit the project default), and `user_stories`.
- `context_md`: plan-specific context only (the issue's own description + a `Source:` header). Project-wide guidance lives in root `AGENTS.md` and is auto-loaded — do not duplicate it.
- `user_stories`: each story has `id` (`US-NNN`, NOT the issue key), `title`, `description`, `acceptance_criteria`, `priority` (int, lower = runs first), `passes` (false initially), `deps` (story IDs within the same plan, from sub-item dependency links).
- See `docs/prd-format.md` for full field semantics, validation rules, and stop conditions.

> **Constraint — documentation acceptance criteria must name an explicit target file.**
> Any `acceptance_criteria` entry that prescribes writing or updating documentation MUST name the exact target as a `path/to/file.md` (add a `:line` anchor when pointing at an existing section). If no canonical file exists yet, the criterion must say to create it at a named path.
> Rationale: in a dogfood batch an agent burned ~75 turns hunting for a "review docs" file that never existed because the criterion never named one — vague targets cause thrash.
> - Allowed: `"Document the off-target marker rule in docs/prd-format.md under the 'Stop Conditions' section"`
> - Forbidden: `"Document the off-target marker rule in the review docs"` and `"note this in the relevant section"` — no file path, so the agent has nothing concrete to target.

If an active batch exists (per Step 4), only rebuild it after the user **positively confirms it was created this session and never started by any session**: write the user-confirmed **combined** set (existing + new issues) with `springfield plan --replace --prd -`. On any doubt — started, unsure, or unknown provenance — stop and send the user through the `springfield:recover` skill first (`springfield status` cannot prove a batch is safe to discard). Never use `--append` for plan-from-issue ingest.

## Tracker profiles

Follow the block matching the tracker selected in Step 0. Each block is the single source of truth for that tracker's fetch mechanism, key parsing, container expansion, sub-item / dependency structure, acceptance-criteria location, and source header. The headings here are also the validation registry for the Step 0 `tracker = "<name>"` check — a value with no matching heading is rejected.

### Tracker profile: jira

- **Fetch (precondition, not setup):** detect, in order — (1) Atlassian MCP (`getJiraIssue`, `searchJiraIssuesUsingJql`), (2) a `jira` CLI on PATH (`jira issue view <KEY>`), (3) Jira REST creds (base URL + API token). Use the first found. None → stop: "No issue-tracker tool detected for jira. Connect the Atlassian MCP, install the `jira` CLI, or set Jira REST credentials, then re-run."
- **Issue reference:** keys look like `PROJ-123`; parse the key from any `.../browse/PROJ-123` URL.
- **Container expansion:** the container is an **epic**. Query children: `parent = <EPIC>` first, then `"Epic Link" = <EPIC>` if empty.
- **Structure:** sub-items = **subtasks** → user stories. Dependency links = `blocks` / `is blocked by`. Ordering signals: backlog rank, then priority.
- **Acceptance criteria location:** (1) a dedicated AC custom field, (2) an `Acceptance Criteria` heading/checklist in the description, (3) none → ask.
- **Source header:** `Source: PROJ-123 — <title>` + `https://<your-domain>.atlassian.net/browse/PROJ-123`.

### Tracker profile: linear

*(Tool names, the `get_issue` call shape, and the `includeRelations` response schema were VERIFIED against the live Linear MCP `tools/list` + a real `get_issue`, 2026-06-23. The `parentId` id-form below is inferred from Linear's GraphQL spec, not that same live session — the zero-children guard is the safety net if it's wrong. The read tools are stable core; if a call fails, introspect the connected tool list — Linear's write/upsert tools churn, e.g. `create_issue`/`update_issue` were merged into `save_issue`.)*

- **Fetch (precondition, not setup):** detect, in order — (1) the **Linear MCP** (hosted `https://mcp.linear.app/mcp`, `Authorization: Bearer <token>` for non-interactive): fetch one issue with `get_issue(id)` — `id` accepts the identifier (e.g. `B2B2C-299`) — and search with `list_issues`. (2) a `linear` CLI on PATH (third-party/unofficial). (3) the Linear GraphQL API with a personal API key. First found wins. None → stop: "No issue-tracker tool detected for linear. Connect the Linear MCP (mcp.linear.app), install a linear CLI, or set a Linear API key, then re-run."
- **Issue reference:** identifiers are `<TEAMKEY>-<n>` where the team key is **alphanumeric** — pattern `[A-Z0-9]+-\d+` (real example `B2B2C-299`: digit *in* the prefix — a letters-only regex silently drops whole teams). No single org-wide prefix; keys are per-team and there are dozens. Parse the key from any `linear.app/<workspace>/issue/<KEY>/...` URL.
- **Container expansion:** Linear has no epic — the container is a **Project** (an Initiative sits above; a Milestone groups issues within a project). Resolve via `list_projects` / `get_project`; expand to its issues with `list_issues(project: <name|id|slug>)`.
- **Structure:** **sub-issues = children → user stories**, via `list_issues(parentId: <id>)` — pass the issue's `id` field from the `get_issue` response (the node id), not necessarily the `B2B2C-299` identifier. A wrong id-form yields a **silent empty set, not an error**, so if a `parentId` query returns zero children, treat it as suspect and re-check before concluding the issue is childless. **Dependencies:** `get_issue(id, includeRelations: true)` returns `relations: { blocks: [...], blockedBy: [...], relatedTo: [...], duplicateOf: ... }` — use `blockedBy` / `blocks` (same-parent only → same-plan deps). Ordering signals: `priority` (0=None, 1=Urgent … 4=Low), then project/cycle order.
- **Acceptance criteria location:** Linear has no first-class AC field — criteria live in the issue **description** markdown (`get_issue` returns the full description; `## Context` / `## Acceptance Criteria` style sections are common, esp. on Jira-mirrored issues opening with "Mirrored from Jira <KEY>"). Look for an AC heading; else ask.
- **Branch name:** every issue carries a **`gitBranchName`** field (e.g. `b2b2c-299-…`) — the canonical Linear branch name. This skill only emits an envelope and never sets a branch, so there's nothing to do with it here; it's noted only because it's the branch name Linear's status automation keys off (see the **Workflow note** below).
- **Source header:** `Source: <KEY> — <title>` + the issue's `linear.app/...` URL.
- **Workflow note:** Linear advances status from linked GitHub activity when the branch carries the ID (use the issue's `gitBranchName`) or the PR body uses a closing keyword (`fixes <KEY>`). Springfield's auto-cut `springfield/batch-<id>` branch does **not** carry the ID, so status won't move on its own — put `fixes <KEY>` in the PR. Per-team config.

## Out of scope

This skill reads the tracker and emits a batch. It does NOT write back to the tracker — no status transitions, no "batch started" comments. Closing that loop is the operator's job.

Keep Springfield as the only user-facing surface.

## Invocation Input

User input from the slash command invocation:

$ARGUMENTS

If `$ARGUMENTS` is empty, continue with the default Springfield behavior for this command.
