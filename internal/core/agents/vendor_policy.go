package agents

// ClaudeHeadlessMetered is the master switch for Springfield's handling of
// Anthropic's `claude -p` (headless) billing policy. It is the single place to
// flip when that policy changes.
//
// Background: on 2026-05-14 Anthropic began metering `claude -p` headless
// invocations separately from Claude Max/Pro subscriptions — drawing from a
// Console-account credit pool, then billing at API rates or rate-limiting
// mid-batch. Springfield responded by demoting claude to opt-in (codex-led
// defaults) and warning at `springfield start`. Anthropic has since reverted
// that change, so `claude -p` once again counts against normal subscription
// limits.
//
// While this is false (the current reality), Springfield treats claude as the
// path of least resistance: claude leads SupportedForExecution() — so it is
// the init picker's pre-checked default and the head of the default
// agent_priority — and `springfield start` does NOT print the billing warning.
//
// If Anthropic re-applies the separate metering, flip this to true. That one
// edit restores BOTH the codex-led default ordering and the start-time billing
// warning; no other code changes are required. It is a var (not a const) so
// the dormant metered-path behavior stays exercised by tests while off.
var ClaudeHeadlessMetered = false
