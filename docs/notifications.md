# Batch terminal-state notifications

An unattended `springfield start` can run for a long time and then quietly stop
— a plan pausing for human review, the batch completing, failing, or halting on
a `--cost-cap`. Without notifications the operator only learns of a terminal
state by polling `springfield status`. This document describes the **notify**
feature: an opt-in hook that surfaces each batch terminal state to the machine
that launched the run, either through the built-in macOS Notification Center
delivery or a command hook you supply for everything else (webhook, ntfy,
Slack).

Notifications are **off by default**. A run with no `[notify]` config fires zero
delivery, and a delivery failure can **never** fail a batch — the notifier
swallows its own errors and logs a warning to stderr.

## When a notification fires

Exactly one notification fires per batch, at whichever terminal state the run
reaches:

| Terminal state | `SPRINGFIELD_NOTIFY_KIND` | Default message |
| -------------- | ------------------------- | --------------- |
| A plan paused for human review, halting the batch | `needs_human` | `Batch <id> needs human review` |
| The batch finished all plans and archived cleanly | `complete` | `Batch <id> completed` |
| The batch halted on an unrecoverable plan failure | `failed` | `Batch <id> failed: <detail>` (the `: <detail>` suffix is omitted when there is no failure message) |
| The batch paused when total spend reached the `--cost-cap` threshold (resumable, not failed) | `cost_capped` | `Batch <id> paused at cost cap ($<spend>)` |

Pre-execution failures — a config that fails to load, an empty
`agent_priority`, anything that stops the batch before the first plan
dispatches — deliberately do **not** notify. Those errors print synchronously
to the terminal the operator just launched from; a notification would
duplicate what is already on screen and would misreport an interactive launch
mistake as a batch failure. `failed` means a batch that *started* and then
died.

## Configuration — the `[notify]` block

Notify targets (a webhook URL, ntfy topic, or Slack hook baked into a command)
are machine-personal, so — like `[review]` — the block lives in the git-ignored
`springfield.local.toml`, beside `springfield.toml`, never in the committed
config. A missing local file is equivalent to `[notify] enabled = false`.

```toml
[notify]
enabled = true                       # opt-in master switch; default false
command = "curl -fsS -d \"$SPRINGFIELD_NOTIFY_MESSAGE\" https://ntfy.sh/my-topic"
```

| Key | Type | Default | Meaning |
| --- | ---- | ------- | ------- |
| `enabled` | bool | `false` | Master switch. When `false` (or the block/file is absent), notifications are disabled and the run stays silent. |
| `command` | string | `""` | Optional user-supplied command run once per event via `sh -c`. Empty falls back to the built-in macOS delivery (see below). |

Unknown keys are rejected: the file is tiny and operator-edited, so a typo like
`eanbled = true` fails loudly with an `unknown keys:` error rather than silently
leaving notifications off.

### How delivery is resolved

`enabled = true` on its own does **not** guarantee a notification — the delivery
target is resolved in this order:

1. **`enabled = false`** (or no `[notify]` block) → no-op. Zero side effects; the opt-in default.
2. **`command` non-empty** → the [command hook](#the-command-hook) runs, on every platform.
3. **`command` empty, on macOS** → the built-in [osascript delivery](#the-macos-default-osascript).
4. **`command` empty, off macOS** → no-op. An enabled-but-command-less config has nothing to deliver off macOS.

## The macOS default (osascript)

On macOS with no `command` set, notifications post to Notification Center via
`osascript`, which ships with the OS — no third-party dependency and nothing to
install. Each event runs the equivalent of:

```
osascript -e 'display notification "<message>" with title "Springfield"'
```

The message is the per-state text from the table above. The batch id and any
failure detail are escaped before they are embedded in the AppleScript string,
so a value containing quotes or backslashes cannot break out of the command.

> The one thing automated tests cannot assert is that a real banner appears —
> that requires an operator to visually confirm a Notification Center banner
> fires on their machine. Delivery uses `osascript` with no extra permissions;
> if banners do not appear, check **System Settings → Notifications** for the
> terminal app running Springfield.

## The command hook

Set `command` to route notifications anywhere else. The command runs **once per
event** via `sh -c "<command>"`, so shell syntax (pipes, quoting, `&&`) works as
written. It inherits the process environment plus the event details, exported as
`SPRINGFIELD_NOTIFY_*` variables:

| Variable | Contents |
| -------- | -------- |
| `SPRINGFIELD_NOTIFY_KIND` | Stable machine-readable state: `needs_human`, `complete`, `failed`, or `cost_capped`. |
| `SPRINGFIELD_NOTIFY_BATCH_ID` | The batch id the event is about. |
| `SPRINGFIELD_NOTIFY_MESSAGE` | The same human-readable text used by the macOS delivery. |
| `SPRINGFIELD_NOTIFY_SPEND_USD` | Batch spend, formatted to two decimals. `0.00` unless the state is `cost_capped`. |
| `SPRINGFIELD_NOTIFY_DETAIL` | The failure message for `failed`; empty otherwise. |

Every variable is **always present** (empty when not applicable), so a command
can rely on the full set without guarding for missing keys — switch on
`SPRINGFIELD_NOTIFY_KIND` for state-specific behavior.

### Example: ntfy

```toml
[notify]
enabled = true
command = "curl -fsS -H \"Title: Springfield $SPRINGFIELD_NOTIFY_KIND\" -d \"$SPRINGFIELD_NOTIFY_MESSAGE\" https://ntfy.sh/my-topic"
```

### Example: Slack incoming webhook

```toml
[notify]
enabled = true
command = "curl -fsS -X POST -H 'Content-type: application/json' -d \"{\\\"text\\\": \\\"$SPRINGFIELD_NOTIFY_MESSAGE\\\"}\" https://hooks.slack.com/services/XXX/YYY/ZZZ"
```

### Failure handling

If the command exits non-zero (or cannot be spawned), Springfield writes
`warning: notify command failed: <err>` to stderr and continues. A notify
failure is never allowed to fail the batch — the run's outcome is unchanged
whether or not delivery succeeds.

## Disabling notifications

Notifications are disabled by default, so no action is needed to keep a run
silent. To turn an enabled configuration off, either:

- set `enabled = false` in the `[notify]` block, or
- remove the `[notify]` block (or the whole `springfield.local.toml`) entirely.

Any of these resolves to the no-op notifier: the terminal-state seam is still
invoked on every run (it is never nil), it just delivers nothing.
