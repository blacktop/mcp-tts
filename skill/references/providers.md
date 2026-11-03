# TTS Provider Reference

Read this file only when selecting non-default tool parameters, applying fallback behavior, or honoring a user-created provider configuration. Treat the current MCP `tools/list` schemas as authoritative if this reference differs.

## Selection policy

1. Use tools already exposed by the connected `mcp-tts` server. Do not shell-probe binaries or credentials.
2. Default to local-only speech. When both local tools are exposed, alternate successful announcements within the session so `voice_tts` and `say_tts` are used roughly equally. Start with the first local provider in configured order; do not persist rotation counters.
3. Use `say_tts` immediately for time-sensitive intervention alerts when needed, then prefer `voice_tts` for the next eligible announcement to restore the balance.
4. When only one local tool is exposed, use it directly instead of the interactive `tts` dispatcher.
5. Use cloud speech only after an explicit user choice or an existing user-created configuration. Select exactly one cloud provider; never probe or chain Google, OpenAI, and ElevenLabs.
6. If the selected cloud provider fails for quota, token, authentication, or configuration reasons, disable automatic cloud TTS for the rest of the session. Use one local fallback if available; otherwise remain text-only. A later explicit user request may select one cloud provider again.
7. Attempt each selected provider at most once and make at most two TTS calls total for one announcement.
8. If speech still fails, preserve the textual result and stop retrying.

No provider is guaranteed. Local audio can be unavailable in CI, containers, Linux, headless/background sessions, muted hosts, or stale MCP server processes.

## Provider choice

When `say_tts` or `voice_tts` is available, use local speech without interrupting the user:

- Both available with no saved preference: alternate successful announcements roughly 1:1, beginning with the first local provider in `provider_order`
- Urgent issue or intervention: `say_tts` may run out of turn; choose `voice_tts` next to restore balance
- Only one available: use that tool
- Explicit preference: honor it instead of balancing; persist the requested priority unless the user says it is only for this call or session

Ask for a provider choice only when:

- The user asks for cloud speech or a hosted voice without naming a provider.
- No local tool is exposed, at least one cloud tool is exposed, and the user still wants spoken output.

When local tools are available, use a neutral prompt:

> "Use private local speech with an available local tool, which consumes no API quota, or use a cloud voice that consumes provider quota?"

When no local tool is available, list only exposed cloud providers and text-only mode:

> "No local speech tool is available. Stay text-only, or use one exposed cloud provider that consumes API quota?"

Default the choice to local when available and text-only otherwise. If no speech tool is exposed, remain text-only without prompting. If the user names a specific provider, that is already an explicit choice and needs no extra prompt. If an existing configuration is ambiguous or names an unavailable provider, use the balanced local default or text-only mode and report the configuration issue non-blockingly instead of prompting. A preference such as “prefer Voice,” “prefer say,” or “use both equally” updates the project configuration while preserving unrelated fields. An explicitly one-off request changes only the current session.

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

Read an existing project configuration if present. For cross-agent setups, prefer `.agents/tts-config.json`; accept legacy `.claude/tts-config.json` for compatibility. When the user states a provider preference, update the existing file while preserving unrelated fields, or create `.agents/tts-config.json` when neither file exists. Do not persist an explicitly one-off request.

Recognized concepts:

- `speaker`: short spoken project or agent label
- `provider_order`: explicit order, normally `["voice_tts", "say_tts"]`
- `local_strategy`: `balanced` (default) or `priority`
- `unavailable_providers`: providers the user has chosen to suppress
- `voices`: optional per-message voice choices

Accept legacy short names (`voice`, `say`, `google`, `openai`, `elevenlabs`) as aliases for their corresponding `_tts` tools. A missing `local_strategy` means `balanced`; `provider_order` chooses the first turn and breaks ties. Set `local_strategy` to `priority` when the user prefers one local provider, or to `balanced` when the user asks to use both equally. Message urgency may still move `say_tts` forward once. If a legacy configuration lists multiple cloud providers, select only the first eligible cloud provider; after any cloud failure, skip every remaining cloud entry for the rest of the session.

Example local-first configuration:

```json
{
  "speaker": "mcp-tts",
  "provider_order": ["voice_tts", "say_tts"],
  "local_strategy": "balanced",
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
