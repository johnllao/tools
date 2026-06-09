# DeepSeek: Subagent `thinking:disabled` + `reasoning_effort` Conflict

## Symptom

All subagents (including `my-agent-hello`) fail with:

```
API Error: 400 thinking options type cannot be disabled when reasoning_effort is set
```

## Root Cause

A confirmed DeepSeek API bug — tracked in [deepseek-ai/DeepSeek-V3#1397](https://github.com/deepseek-ai/DeepSeek-V3/issues/1397).

| Layer | What it sends |
|---|---|
| **Claude Code ≥ 2.1.166** | Hardcodes `thinking: { type: "disabled" }` for all subagents (subagents don't need to display thinking to the user) |
| **DeepSeek model config** | Injects `reasoning_effort` (from model selection like `deepseek-v4-pro[1m]`) |
| **DeepSeek API endpoint** | Treats the two as mutually exclusive → returns 400 |

Claude Code 2.1.165 and earlier did **not** hardcode `thinking: disabled` on subagents, so this wasn't an issue before.

## Our Config

From `~/.claude/settings.json`:

```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "https://api.deepseek.com/anthropic",
    "ANTHROPIC_MODEL": "deepseek-v4-pro[1m]",
    "CLAUDE_CODE_SUBAGENT_MODEL": "deepseek-v4-flash"
  },
  "model": "sonnet"
}
```

The `ANTHROPIC_MODEL` and `CLAUDE_CODE_SUBAGENT_MODEL` imply `reasoning_effort` is active, while Claude Code's subagent framework sends `thinking: disabled` — causing the conflict.

## Workarounds

1. **Downgrade Claude Code** to ≤ 2.1.165 and pin the version.
2. **Use a proxy** that strips either `thinking` or `reasoning_effort` from requests before forwarding to DeepSeek.
3. **Remove `reasoning_effort`** from config — may degrade main-agent reasoning quality.
4. **Wait for DeepSeek to fix** the API endpoint (issue is open and acknowledged).

## Related Issues

- [deepseek-ai/DeepSeek-V3#1397](https://github.com/deepseek-ai/DeepSeek-V3/issues/1397) — Main issue
- [deepseek-ai/DeepSeek-V3#1376](https://github.com/deepseek-ai/DeepSeek-V3/issues/1376) — Similar conflict: `thinking` vs `tool_choice`

## Date Identified

2026-06-08
