---
name: speak
description: "Speaks a short digest through the mcp-tts MCP server whenever the user would otherwise have to be watching the screen to know something happened. Select it while composing the reply in two cases: you are writing a summary at a stopping point (a substantial task finished, a plan is ready, a long build/test/deploy/research phase changed state, a lengthy final result needs an audible digest), or you need the user (a question, a decision, approval, authentication, a manual action, a terminal blocker, an exhausted retry budget). Nothing announces on the agent's behalf, so an unspoken summary is a silent one. Do not use for routine progress, short answers, or intermediate steps."
---

# Speak

Use speech as an attention channel for meaningful agent transitions. Keep the complete result, evidence, and required interaction in text; audio only alerts the user or gives a short digest.

## Non-negotiable rules

- Preserve task truth. Never announce success while checks failed, were skipped, remain pending, or could not run.
- Never let a TTS failure change whether the main task continues, stops, succeeds, or fails.
- Never speak secrets, tokens, credentials, private keys, environment values, proprietary source, raw logs, stack traces, personal data, or full sensitive identifiers.
- Respect quiet requests. Stay silent in CI, headless sessions, non-interactive automation, or when no audio tool is available.
- Deduplicate announcements. Speak once per meaningful transition, blocker, or request for help.
- Make no persistent configuration changes unless the user explicitly asks.

## When to reach for this

Nothing announces on your behalf. No hook watches for finished turns or reads
your summary; if you do not elect to speak, the user hears nothing. Make that
election while you are composing the reply, not after sending it.

Two moments carry the obligation:

- **You are writing a summary at a stopping point.** A substantial task is done
  and you are handing back a wrap-up meant to be read. Speak a digest of it.
- **You need something from the user.** You are asking a question, presenting a
  decision, or blocked pending approval or manual action. Speak the ask.

Everything else stays silent. A short answer, a routine edit, a passing test, or
an intermediate step is not a stopping point.

## Decide whether to speak

| Event | Mode | Action |
|---|---|---|
| A substantial plan is finalized | Notify | Announce the objective and major phases, then continue |
| A long-running build, test, deployment, release, research, or monitoring operation changes phase | Notify | Announce a meaningful status transition, then continue |
| A major task completes or its final handoff is lengthy | Notify | Announce the outcome and where details are available |
| A transient failure has a safe recovery path | Silent | Recover within the retry budget |
| A fallback succeeds after a meaningful failure | Notify if useful | State the degraded path or limitation, then continue |
| A retry budget is exhausted or a terminal failure remains | Intervene | State the blocker and exact help needed |
| Approval, authentication, a consequential decision, manual action, or external-state change is required | Intervene | Announce once, provide the full request in text, then stop |
| A wait or monitoring poll is unchanged | Silent | Keep waiting without repeated announcements |
| A routine edit, tool call, short answer, or intermediate test passes | Silent | Continue normally |

## Long-running work

Do not emit spoken heartbeats for every poll or tool call. Announce a checkpoint only when at least one of these is true:

- A major phase completed or the expected completion time materially changed.
- Roughly ten minutes of extended work passed without a spoken update and there is a meaningful delta.
- A long operation completed, failed, timed out, or now requires human action.

Limit long-running updates to roughly one every ten minutes unless a new blocker requires immediate attention. Say what changed, what happens next, and whether the user is needed.

## Human intervention

Intervene when the agent cannot safely or usefully continue without the user:

- The next action needs new authority or approval.
- A consequential choice would materially change scope, behavior, cost, or risk.
- Authentication, a credential rotation, a GUI/device action, or another manual step is required.
- A non-retryable permission, configuration, missing-tool, or unsupported-environment error blocks progress.
- A bounded retry budget is exhausted with no distinct safe recovery path.
- An external dependency must change state before work can resume.

For the same transient, idempotent failure, make at most two safe recovery attempts. Do not retry authentication failures, permission denials, invalid arguments, missing tools, unsupported sessions, or destructive operations merely to avoid asking for help.

An intervention announcement must contain:

1. The project or agent identity.
2. The blocked outcome in plain language.
3. The exact decision or action needed.
4. Whether work is paused or a safe fallback is continuing.

Speech never replaces the written question or approval request. Follow the host's normal interaction rules, then wait when user input is genuinely blocking.

## Build the spoken message

Start with a short source label:

> "<project or agent> says: <outcome>. <next action or help needed>."

Use the repository name by default. If several agents share a repository and a task/session label is already known, include that label. Do not run broad discovery or persist a label solely for speech.

Keep messages to 20–50 words and one to three short sentences. Include only:

- The outcome or current state.
- A failed, skipped, or pending check when it changes the truth.
- The next action, or the exact help needed.

If the textual handoff will exceed roughly 200 words or contains several findings, speak only a digest. Never read a full summary, diff, file list, command transcript, log, stack trace, URL, or test matrix aloud.

Transform text for speech:

- Replace paths with a filename or component name.
- Replace hashes and opaque IDs with their purpose.
- Replace URLs with “the link in chat.”
- Replace long lists with a count and the most important item.
- Expand ambiguous abbreviations when pronunciation matters.
- Use calm, direct language; do not dramatize failures.

## Choose a TTS tool

Use only tools present in the current MCP tool catalog. Do not probe `PATH`, credentials, environment variables, or shell startup files.

- **Balanced local default:** when both `say_tts` and `voice_tts` are exposed, alternate successful announcements within the session so each is used roughly equally. Start with the first local provider in the configured order and keep the rotation session-local.
- **Urgent intervention:** `say_tts` may go first because it starts quickly. Prefer `voice_tts` for the next eligible announcement to restore the balance.
- **Single local tool:** when only one local tool is exposed, use it directly. Do not call the interactive `tts` dispatcher or try cloud providers speculatively.
- **Cloud providers:** use only the one provider explicitly selected by the user or an existing user-created configuration, and never for sensitive material. Do not fan out across cloud providers.
- **Cloud failure:** on quota, token, authentication, or configuration failure, disable automatic cloud TTS for the rest of the session and use a local tool if available; otherwise remain text-only. A later explicit user request may select one cloud provider again.

Do not ask the user to choose a provider merely because several tools are exposed—the privacy-preserving, zero-API-token balanced local behavior is the default. Ask once only when the user requests a cloud voice without naming a provider, or when no local tool is available, at least one cloud tool is exposed, and spoken output is still desired. When the user states a provider preference or priority order, update the project configuration while preserving unrelated fields. Keep an explicitly one-off request session-local.

Treat the live MCP input schema as authoritative. Read [references/providers.md](references/providers.md) before selecting non-default parameters, applying fallback behavior, or honoring an existing provider configuration.

For optional per-project voice identity explicitly requested by the user, also read [references/voice-pools.json](references/voice-pools.json).

## Rex speaking indicator

Builds of mcp-tts with Rex support report **🔊 Speaking** automatically through Rex's [program-status protocol](https://www.superlogical.com/rex/docs/build/program-status). Call the TTS tool normally and let the server clear its own status when playback ends. A tool response can arrive while audio is still playing, so do not rename tabs, move focus, or emit or clear terminal status around the call.

If the indicator is missing, the MCP server needs the updated binary, inherited `REX_SESSION` and `REX_BLOCK`, and its controlling terminal. Rex's **Settings → Notifications → Program Activity → Show activity in tabs** must also be enabled. A detached or shared server without the caller's terminal cannot safely identify the speaking pane; keep the spoken source label and do not guess a tab. Use the Rex skill for further diagnosis when requested.

## Workflow

1. Classify the event as silent, notify, or intervene.
2. Write the required user-facing status, result, or question in the normal channel.
3. Create a 20–50 word speech-safe digest with the source label.
4. Select the next available local tool using the configured preference or balanced rotation; use cloud only by explicit prior choice.
5. Attempt speech within the bounded fallback policy, then continue or stop according to the main task—not according to TTS success.

## Examples

**Long operation completed**

> "mcp-tts says: The release build finished and all required checks passed. The full artifact list is in chat."

**Lengthy final handoff**

> "ipsw says: The restore review is complete. I found two blocking safety issues and one follow-up; the evidence and file locations are in chat."

**Blocking intervention**

> "mcp-tts says: The Homebrew publication needs a new GitHub token. I posted the exact rotation steps and paused before tagging the release."

**Retry budget exhausted**

> "deployment agent says: The deploy failed twice with the same permission denial. I need access restored before I can continue."

**Long-running checkpoint**

> "firmware analysis says: Extraction finished and binary analysis is underway. No input is needed; I’ll speak again only at completion or if blocked."

## Completion check

Before returning:

- Was this transition important enough to interrupt the user?
- Does the spoken message match the verified textual truth?
- Is it under 50 words and free of sensitive details?
- If help is required, does it name one exact action and say that work is paused?
- Has this same transition already been announced?

If any answer is wrong, revise the message or stay silent.
