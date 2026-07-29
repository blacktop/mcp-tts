# TTS Provider Reference

Read this file only when selecting non-default tool parameters, applying fallback behavior, or honoring a user-created provider configuration. Treat the current MCP `tools/list` schemas as authoritative if this reference differs.

## Selection policy

1. Use tools already exposed by the connected `mcp-tts` server. Do not shell-probe binaries or credentials.
2. Default to local-only speech. When either local tool is exposed, call it directly instead of the interactive `tts` dispatcher.
3. Use `say_tts` for time-sensitive intervention alerts.
4. Use `voice_tts` for plans, summaries, and non-urgent announcements when registered.
5. Use cloud speech only after an explicit user choice or an existing user-created configuration. Select exactly one cloud provider; never probe or chain Google, OpenAI, and ElevenLabs.
6. If the selected cloud provider fails for quota, token, authentication, or configuration reasons, disable automatic cloud TTS for the rest of the session. Use one local fallback if available; otherwise remain text-only. A later explicit user request may select one cloud provider again.
7. Attempt each selected provider at most once and make at most two TTS calls total for one announcement.
8. If speech still fails, preserve the textual result and stop retrying.

No provider is guaranteed. Local audio can be unavailable in CI, containers, Linux, headless/background sessions, muted hosts, or stale MCP server processes.

## Provider choice

When `say_tts` or `voice_tts` is available, use the local default without interrupting the user:

- Urgent issue or intervention: `say_tts`
- Plan, checkpoint, or summary: `voice_tts`, then `say_tts` if Voice is not registered

Ask for a provider choice only when:

- The user asks for cloud speech or a hosted voice without naming a provider.
- No local tool is exposed, at least one cloud tool is exposed, and the user still wants spoken output.

When local tools are available, use a neutral prompt:

> "Use private local speech with an available local tool, which consumes no API quota, or use a cloud voice that consumes provider quota?"

When no local tool is available, list only exposed cloud providers and text-only mode:

> "No local speech tool is available. Stay text-only, or use one exposed cloud provider that consumes API quota?"

Default the choice to local when available and text-only otherwise. If no speech tool is exposed, remain text-only without prompting. If the user names a specific provider, that is already an explicit choice and needs no extra prompt. If an existing configuration is ambiguous or names an unavailable provider, use the local default or text-only mode and report the configuration issue non-blockingly instead of prompting. A one-off correction changes only the current session. Update a configuration file only when the user says to remember the choice, make it the default, or directly asks to edit the configuration.

## Local tools

### `say_tts`

Fast local macOS speech with no API key:

- `text`: required string
- `voice`: optional exact installed macOS voice
- `rate`: optional integer from 50–500

Leave `voice` unset by default so macOS uses the configured System Voice. Use an explicit voice only after the user asks for voice identity and the exact name is known to be installed.

### `voice_tts`

Local Qwen3-TTS through MLX with no API key. This tool is registered only when `voice-say` is resolvable by the server, so its presence in `tools/list` is the availability check.

- `text`: required string
- `voice`: optional `Ryan` or `Aiden`
- `tier`: optional `small` or `large`
- `style`: optional free-text delivery guidance
- `describe`: optional free-text voice description

Rules:

- `voice` and `describe` conflict; never send both.
- `describe` forces the larger VoiceDesign model.
- Prefer `tier: small` for routine announcements.
- Pass a short `style` instead of lengthening the spoken text.
- Model startup costs several seconds. Do not use it for urgent approvals, terminal blockers, or time-critical alerts.
- Playback is direct. It cannot satisfy no-play or audio-file-output requests.
- If the tool is absent, fall back to `say_tts` without probing `PATH`.

## Cloud tools

`google_tts`, `openai_tts`, and `elevenlabs_tts` send text off-host. Use them only after explicit selection and only for content safe to disclose to that provider.

- Never inspect environment variables or shell startup files for keys.
- Never include source code, credentials, raw incident logs, customer data, or sensitive identifiers.
- Use the live MCP schema for current model, voice, speed, and instruction values.
- On authentication, configuration, quota, or rate-limit failure, disable cloud TTS for the session. Use local speech if available; otherwise remain text-only.
- An ElevenLabs `402 paid_plan_required` result may mean the configured voice is unavailable on the current plan; do not repeatedly retry it.

## Error classification

| Failure | Response |
|---|---|
| Invalid argument or schema rejection | Correct once only when the fix is clear |
| Authentication, missing key, or permission denial | Do not retry; use local speech if available, otherwise text only |
| Missing tool or unsupported environment/session | Do not retry; use another already-exposed local tool |
| Rate limit, quota, or transient cloud outage | Disable cloud for the session; use one local fallback if available |
| Audio routing or playback failure | Try one other local tool only if it will not delay intervention |
| Context cancellation | Stop speech immediately |
| Unknown failure | Preserve text and stop after the total attempt budget |

Do not announce a TTS provider failure unless it changes the main task outcome or requires user action.

When a saved cloud choice fails, report in text that the session switched to local or text-only mode and that the saved configuration was not changed. If the user responds with “remember local” or equivalent, update `provider_order` to the local-first default.

## Existing configuration

Read an existing user-created project configuration if present. For cross-agent setups, prefer `.agents/tts-config.json`; accept legacy `.claude/tts-config.json` for compatibility. Never create or modify either file unless the user explicitly asks.

Recognized concepts:

- `speaker`: short spoken project or agent label
- `provider_order`: explicit order, normally `["voice_tts", "say_tts"]`
- `unavailable_providers`: providers the user has chosen to suppress
- `voices`: optional per-message voice choices

Accept legacy short names (`voice`, `say`, `google`, `openai`, `elevenlabs`) as aliases for their corresponding `_tts` tools. Message urgency still controls which configured local tool runs first. If a legacy configuration lists multiple cloud providers, select only the first eligible cloud provider; after any cloud failure, skip every remaining cloud entry for the rest of the session.

Example local-first configuration:

```json
{
  "speaker": "mcp-tts",
  "provider_order": ["voice_tts", "say_tts"],
  "unavailable_providers": []
}
```

Configuration never overrides live tool availability, privacy rules, the two-attempt budget, or an explicit quiet request. A cloud provider in `provider_order` counts as explicit selection, but cloud failure disables automatic cloud entries for the session and falls back to the first suitable local provider or text-only mode. A later explicit user request may select one cloud provider again.

## Optional voice identity

Only configure distinct voices when the user asks:

1. Read `voice-pools.json`.
2. Compare candidates with the live tool schema or exact installed macOS voices.
3. Avoid reusing a voice only when the user wants audible project distinction.
4. Present the proposed assignment before persisting it.
5. Persist only with explicit approval.
