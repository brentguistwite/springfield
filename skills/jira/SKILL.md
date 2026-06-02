---
name: jira
description: Use Springfield jira to compile one or more Jira tickets, or an epic and its children, into a runnable batch.
---

# Springfield Jira

Use Springfield jira to compile one or more Jira tickets, or an epic and its children, into a runnable batch.

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

Compile a Springfield batch from one or more Jira tickets, or an epic and its child tickets.

Read project guidance from AGENTS.md first, then CLAUDE.md, then GEMINI.md when present.

This skill emits the SAME PRD envelope as the `plan` skill (see `docs/prd-format.md`) and pipes it to `springfield plan --prd -`. Its only added surface is reaching Jira and mapping tickets onto the envelope — the Definition-of-Done and pre-merge-review steps are identical to the `plan` skill.

## Step 1 — Confirm a Jira tool is available (precondition, not setup)

Springfield does NOT manage Jira access. Detect whatever the running agent already has, in this order:

1. An Atlassian MCP server (tools named like `getJiraIssue`, `searchJiraIssuesUsingJql`).
2. A `jira` CLI on PATH (`jira issue view <KEY>`).
3. Jira REST credentials (a base URL + API token the agent can curl).

Use the first one found. If none is available, stop and tell the user: "No Jira tool detected. Connect the Atlassian MCP, install the `jira` CLI, or set Jira REST credentials, then re-run." Do not attempt to install or authenticate anything.

## Step 2 — Determine input

Ask the user once: "Which tickets? List ticket keys (e.g. PROJ-123), paste ticket URLs, or give me an epic key."

- Parse the key out of any `.../browse/PROJ-123` URL.
- If unsure whether a key is an epic, fetch it and check its issue type — do not guess epic-vs-ticket from one ambiguous input.

## Step 3 — Resolve scope (epic expansion)

For bare ticket keys, fetch each directly.

For an epic key, query its children with the detected tool. Field names vary by Jira instance — try `parent = <EPIC>` first, then `"Epic Link" = <EPIC>` if the first returns nothing.

**Guardrail:** after resolving children, show the discovered list (key + title, in resolved order) and ask "These N tickets — right?" before the heavier per-ticket fetch. This catches wrong-field / wrong-epic results before they become a batch.

## Step 4 — Check for active batch

Run `springfield status` to check whether an active batch already exists.

- If an active batch exists and any plan is `running`, tell the user to wait before replacing.
- If an active batch exists but nothing is running, ask the user: replace it, append to it, or keep it.

**Append is order- and audit-limited — prefer replace when either matters.** `--append` only adds the new plans as phases *after* the existing ones; it does NOT re-run the topological sort across the combined graph, so an appended ticket that `blocks` a plan already queued will still run *after* it. `--append` also keeps the original batch's `source.md` and drops the appended tickets' raw `source`, so they vanish from the audit trail. Therefore: if any incoming ticket has a `blocks` / `is blocked by` relationship with a plan in the active batch, or the user wants a complete `source` audit, recompile the **combined** set with `--replace` (so the topo sort and source cover old + new) rather than appending. Reserve `--append` for independent tickets with no cross-batch dependency.

## Step 5 — Map tickets to the envelope

Grain:

- The epic (or the set of standalone tickets) → the **batch**.
- Each child / standalone ticket → one **plan**. Slug the plan `id` from the ticket key: lowercase it and replace any character outside `[a-z0-9-]` with a hyphen, collapsing runs (`PROJ-123` → `proj-123`; `MY_PROJECT-45` → `my-project-45`). The envelope requires `^[a-z0-9][a-z0-9-]*$`. This keeps the envelope, `.springfield/plans/<id>/`, and the Jira ticket aligned by key.
- A ticket's **subtasks → user stories** in that plan. A ticket with no subtasks → one synthetic user story covering the ticket itself.
- **Don't lose the parent ticket's own Definition of Done.** Only `user_stories[].acceptance_criteria` is consumed by the executor and the review gate — `context_md` is context, never a done-gate. So when a ticket has subtasks AND its own acceptance criteria, the subtasks become the implementation stories AND you append one final synthetic **parent-acceptance** story whose `acceptance_criteria` are the parent ticket's, with `deps` on every subtask story and a `priority` higher than all of them (so it runs last; `priority` must be `>= 1`, never the `0` zero-value). Never strip the parent's criteria into `context_md` oblivion when subtasks exist — they must ride in a story the runner actually checks.
- Story IDs are `US-001`, `US-002`, … per plan (the envelope requires `^US-\d{3,}$`; the ticket key lives in context, not the story id).

Ordering and dependencies (honor the structure Jira already holds):

- **Plan order** within the single serial phase: topologically sort tickets by `blocks` / `is blocked by` links; where there is no link, fall back to Jira backlog rank, then priority, then the order the user listed them.
- **Story deps** (`user_stories[].deps`): a `blocks` link between two subtasks of the **same** parent ticket → a dep within that plan. **Skip** any `blocks` link whose target subtask belongs to a different parent ticket — the envelope forbids cross-plan deps, so a cross-ticket link is a plan-order edge at most, never a story dep (emitting it produces a `dep not found in same plan` hard error).
- A dependency cycle → degrade, never fail. At the **plan** level (`A blocks B blocks A`) drop the cyclic ordering edge and keep rank order. At the **story** level (two subtasks blocking each other) drop **both** `deps` links and emit the stories in rank order — a cyclic `deps` passes envelope validation but leaves no eligible story, so the runner hard-fails the plan with `story dependency graph blocked: no eligible story`. Note the degradation either way.

Text sinks:

- `plans[].context_md`: the ticket description prose with the acceptance-criteria section removed (those become `acceptance_criteria`), prefixed with a one-line `Source: PROJ-123 — <title>` plus the ticket URL. Do NOT include comments. Do NOT duplicate project-wide guidance (root AGENTS.md is auto-loaded by the runner). Keep it bounded: `context_md` warns past ~32 KB and hard-errors past 256 KB, so for a long ticket — pasted logs, stack traces, incident dumps — summarize or clip the description to the decision-relevant prose and leave the full raw text to `source` (below).
- `source` (batch-level, stored as `source.md` for audit): the concatenated raw fetched tickets (key, title, description, acceptance criteria).

## Step 6 — Definition of Done (per story)

Acceptance criteria are PROMPT INPUT, not a deterministic done-gate — the same contract as the `plan` skill. Extract them per story, in order:

1. A dedicated acceptance-criteria custom field on the ticket/subtask.
2. An `Acceptance Criteria` heading or checklist in the description.
3. None found.

Then, per story:

- **Criteria found** → show them back read-only and ask "edit any?". Do not silently re-draft what the team wrote in Jira.
- **None found** → ask one focused question: "how do we know this story is done — what command or observable proves it?" Then draft concrete, checkable criteria and show them for confirmation.

Aim each criterion at something checkable: a test command (e.g. `go test ./auth passes`), a file path, or an HTTP response (e.g. `GET /health returns 200`). Springfield emits a non-fatal warning at ingest on any criterion with no such signal — vague criteria still compile, they just get a nudge.

**Bulk escape hatch:** if the user asks to ingest without per-story questions (e.g. "ingest the whole epic, don't ask me about criteria"), skip the interactive prompts: where extraction yields at least one criterion, take it as-is and let the ingest `[warn]` flag weak phrasing. But an **empty** `acceptance_criteria` list is a HARD validation error that aborts ingest, not a warning — so for a story where extraction found nothing, still emit one minimal checkable criterion drawn from the ticket itself (its title/description) rather than an empty list. Do not invent elaborate criteria the team did not write.

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

Show the user the proposed plans (one line per plan: `proj-123 — <title>`).
Ask for confirmation before writing.

Once confirmed, compile a **PRD envelope** and pipe it to `springfield plan --prd -`:

```bash
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
```

Schema notes:
- `phases`: execution ordering. Each phase has `mode` (`"serial"`) and `plans` (list of plan IDs in that phase, in run order).
- `plans`: each plan has `id` (slugged from the ticket key, lowercased), `title`, `description`, optional `context_md`, optional `review` (the per-plan toggle from Step 7 — omit to inherit the project default), and `user_stories`.
- `context_md`: plan-specific context only (the ticket's own description + a `Source:` header). Project-wide guidance lives in root `AGENTS.md` and is auto-loaded — do not duplicate it.
- `user_stories`: each story has `id` (`US-NNN`, NOT the ticket key), `title`, `description`, `acceptance_criteria`, `priority` (int, lower = runs first), `passes` (false initially), `deps` (story IDs within the same plan, from subtask blocks links).
- See `docs/prd-format.md` for full field semantics, validation rules, and stop conditions.

> **Constraint — documentation acceptance criteria must name an explicit target file.**
> Any `acceptance_criteria` entry that prescribes writing or updating documentation MUST name the exact target as a `path/to/file.md` (add a `:line` anchor when pointing at an existing section). If no canonical file exists yet, the criterion must say to create it at a named path.
> Rationale: in a dogfood batch an agent burned ~75 turns hunting for a "review docs" file that never existed because the criterion never named one — vague targets cause thrash.
> - Allowed: `"Document the off-target marker rule in docs/prd-format.md under the 'Stop Conditions' section"`
> - Forbidden: `"Document the off-target marker rule in the review docs"` and `"note this in the relevant section"` — no file path, so the agent has nothing concrete to target.

Use `--replace` or `--append` if an active batch exists (per Step 4) — prefer `--replace` on the combined set whenever cross-batch `blocks` links or a complete `source` audit matter, since `--append` neither reorders across the existing batch nor preserves appended source.

## Out of scope

This skill reads Jira and emits a batch. It does NOT write back to Jira — no status transitions, no "batch started" comments. Closing that loop is the operator's job.

Keep Springfield as the only user-facing surface.
