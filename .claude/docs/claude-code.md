# Claude Code Reference

> A comprehensive reference covering keybindings, MCP specification, permission settings, and best practices.

---

## Table of Contents

- [Keybindings & Special Characters](#keybindings--special-characters)
  - [Navigation & Editing](#navigation--editing)
  - [Special Input Prefixes](#special-input-prefixes)
  - [Common Slash Commands](#common-slash-commands)
  - [Multi-line Editing](#multi-line-editing)
- [MCP (Model Context Protocol) Specification](#mcp-model-context-protocol-specification)
  - [Specification Versions](#specification-versions)
  - [Architecture](#architecture-3-roles)
  - [Transports](#transports)
  - [JSON-RPC 2.0 Wire Format](#json-rpc-20-wire-format)
  - [Core Primitives](#core-primitives)
  - [Key Specification Changes](#key-specification-changes-2025-2026)
  - [Future Directions](#future-directions)
- [Permission Settings — Tool Names](#permission-settings--tool-names)
  - [Built-in Tools](#built-in-tools)
  - [MCP Tools](#mcp-tools-server-defined)
- [Claude Code Best Practices](#claude-code-best-practices)
  - [Quick-Start Checklist](#quick-start-checklist)
  - [CLAUDE.md](#1-claudemd--persistent-project-context)
  - [Plan Mode](#2-plan-mode--dont-skip-on-complex-tasks)
  - [Context Management](#3-context-management)
  - [Effective Prompting](#4-effective-prompting)
  - [Hooks](#5-hooks--deterministic-automation)
  - [Parallel Sessions](#6-parallel-sessions)
  - [Subagents](#7-subagents-for-specialized-work)
  - [Keyboard Shortcuts](#8-keyboard-shortcuts)
  - [Pitfalls to Avoid](#9-pitfalls-to-avoid)
  - [Cost Optimization](#10-cost-optimization)

---

# Keybindings & Special Characters

## Navigation & Editing

| Key               | Action                     |
|-------------------|----------------------------|
| **Enter**         | Submit the prompt          |
| **Shift+Enter**   | Insert a newline           |
| **Up/Down arrows**| Command history            |
| **Ctrl+A / Home** | Beginning of line          |
| **Ctrl+E / End**  | End of line                |
| **Ctrl+U**        | Clear current line         |
| **Ctrl+C**        | Cancel / interrupt Claude  |
| **Ctrl+D**        | Exit Claude Code           |
| **Ctrl+L**        | Clear screen               |
| **Tab**           | Autocomplete               |
| **Ctrl+Left/Right** | Jump word-by-word        |
| **Ctrl+K**        | Cut cursor to end of line  |
| **Ctrl+W**        | Cut word backward          |
| **Ctrl+Y**        | Paste cut text (yank)      |
| **Ctrl+Backspace**| Delete word backward       |

## Special Input Prefixes

| Prefix | Action |
|--------|--------|
| **`!`** | Run a shell command directly (e.g., `! git status`) |
| **`/`** | Open slash command menu |
| **`#`** | Force file context insertion |

## Common Slash Commands

| Command           | Action                     |
|-------------------|----------------------------|
| `/help`           | Show help                  |
| `/clear`          | Clear conversation         |
| `/model`          | Switch models              |
| `/config`         | Open settings              |
| `/usage`          | Show usage/limits dialog   |
| `/effort`         | Adjust thinking effort     |
| `/review`         | Review current PR/diff     |
| `/security-review`| Run security review        |
| `/fix`            | Apply a suggested fix      |
| `/simplify`       | Simplify/refactor code     |
| `/compact`        | Compress conversation      |
| `/loop`           | Run a prompt on interval   |

## Multi-line Editing

| Input              | Action                     |
|--------------------|----------------------------|
| **"""..."""**      | Multi-line prompt (triple quotes) |
| **Paste**          | Detects pasted vs typed input |

---



# MCP (Model Context Protocol) Specification

**Reference:** [spec.modelcontextprotocol.io](https://spec.modelcontextprotocol.io/specification/2025-11-25/basic) — [GitHub](https://github.com/modelcontextprotocol/specification)

## Specification Versions

| Version | Key Changes |
|---|---|
| `2024-11-05` | Original release (legacy HTTP+SSE transport) |
| `2025-03-26` | Major revision — **Streamable HTTP** introduced, deprecated old HTTP+SSE |
| `2025-06-18` | Refined transport, **JSON-RPC batching** support added |
| `2025-11-25` | Latest stable — stricter request association, `_meta` fields, icons |

## Architecture (3 Roles)

```
Host (Claude, IDE)  ↔  Client (one per server)  ↔  Server (github, fs, db)
                                JSON-RPC 2.0
                          STDIO or Streamable HTTP
```

### Lifecycle
1. **Initialize** — version + capability negotiation
2. **List** — discover tools/resources/prompts
3. **Operate** — call tools, read resources, get prompts
4. **Notifications** — bidirectional event stream
5. **Shutdown** — close transport

## Transports

### STDIO (local/CLI)
- Server runs as a **subprocess** of the client.
- Messages over stdin/stdout, newline-delimited JSON.
- Zero network surface.

### Streamable HTTP (remote) — Current Standard
- Single MCP endpoint supporting POST, GET, DELETE.
- POST: JSON-RPC message → server responds with JSON or SSE stream.
- GET: server-to-client SSE channel.
- DELETE: terminate session.
- Optional `Mcp-Session-Id` header.
- OAuth 2.0 recommended auth.
- Server must validate `Origin` header.

### HTTP+SSE (Legacy, deprecated)
- From `2024-11-05`. Separate SSE endpoint + POST endpoint.

## JSON-RPC 2.0 Wire Format

### Request
```json
{"jsonrpc":"2.0", "id":"req-001", "method":"tools/call", "params":{"name":"create_issue","arguments":{...}}}
```

### Response
```json
{"jsonrpc":"2.0", "id":"req-001", "result":{"content":[{"type":"text","text":"Done"}],"isError":false}}
```

### Error
```json
{"jsonrpc":"2.0", "id":"req-001", "error":{"code":-32602,"message":"Invalid params"}}
```

### Notification (no id)
```json
{"jsonrpc":"2.0", "method":"notifications/message", "params":{"level":"info","message":"Processing..."}}
```

### Batching (2025-06-18+)
Multiple messages in a single HTTP POST as an array.

## Core Primitives

| Primitive | Methods | Description |
|---|---|---|
| **Tools** | `tools/list`, `tools/call` | Callable functions the AI invokes |
| **Resources** | `resources/list`, `resources/read` | Read-only data (files, DB records) |
| **Resource Templates** | `resources/templates/list` | Parameterized URIs |
| **Prompts** | `prompts/list`, `prompts/get` | Reusable prompt templates |

## Key Specification Changes (2025–2026)

| SEP | Change |
|---|---|
| **SEP-2260** | Server-to-client requests must be associated with an originating client request. |
| **SEP-1612** (draft) | Pure HTTP transport — no JSON-RPC, RESTful methods. |
| **2025-11-25** | `_meta` fields, icon support, refined sessions. |

## Future Directions
- **Pure HTTP Transport (SEP-1612)** — RESTful mapping without JSON-RPC
- **gRPC Transport** — awaiting payload/RPC decoupling
- **Session clarity** — ongoing work on Streamable HTTP session semantics

---

# Permission Settings — Tool Names

Use these in `.claude/settings.local.json` under `allowedTools` or when granting/denying permissions.

## Built-in Tools

| Tool | Purpose |
|------|---------|
| `Bash` | Execute shell commands |
| `Read` | Read files |
| `Edit` | Edit files (line-level replacement) |
| `Write` | Create/overwrite files |
| `NotebookEdit` | Edit Jupyter notebooks |
| `Agent` | Spawn subagents |
| `WebSearch` | Search the web |
| `WebFetch` | Fetch and parse a URL |
| `Workflow` | Run multi-agent orchestration scripts |
| `Skill` | Invoke a registered skill |
| `CronCreate` | Schedule recurring/one-shot tasks |
| `CronDelete` | Cancel a scheduled task |
| `CronList` | List scheduled tasks |
| `ScheduleWakeup` | Resume work in dynamic `/loop` mode |
| `TaskCreate`, `TaskGet`, `TaskList`, `TaskOutput`, `TaskStop`, `TaskUpdate` | Task tracking operations |
| `AskUserQuestion` | Prompt the user with a choice |
| `EnterPlanMode`, `ExitPlanMode` | Plan mode workflow |
| `EnterWorktree`, `ExitWorktree` | Git worktree isolation |

## MCP Tools (server-defined)

MCP-connected servers expose their own tools — names depend on the server. Common examples: `gh` (GitHub CLI), filesystem tools, database tools. Run `/fewer-permission-prompts` to auto-allowlist the ones you use regularly.

---

# Claude Code Best Practices

## Quick-Start Checklist

| # | Action | Time | Impact |
|---|--------|------|--------|
| 1 | Write a `CLAUDE.md` for your project | 10 min | 🔥🔥🔥🔥🔥 |
| 2 | Set up `.claudeignore` (like `.gitignore`) | 2 min | 🔥🔥🔥 |
| 3 | Configure permission allowlists (`/permissions`) | 5 min | 🔥🔥🔥🔥 |
| 4 | Add a `PostToolUse` hook for auto-formatting | 5 min | 🔥🔥🔥🔥 |
| 5 | Use Plan Mode for anything >3 files (Shift+Tab) | — | 🔥🔥🔥🔥🔥 |
| 6 | Give Claude a way to verify its own work | — | 🔥🔥🔥🔥🔥 |

---

## 1. CLAUDE.md — Persistent Project Context

Set this up early. It's read at the start of every session and is the single highest-impact configuration.

**What to include:**
- Build / test / lint commands (most important!)
- Tech stack and architecture overview
- Coding conventions and style rules
- "Gotchas" and things NOT to do

**Pro tips:**
- Generate a starter with `/init`, then cut the result in half. Every line must pass: *"Would Claude make a mistake without this?"*
- After Claude makes a mistake, say: *"Update your CLAUDE.md so you never do that again"* — it writes excellent rules for itself.
- Use `.claude/rules/` for topic-specific rules with path-based loading (e.g., TypeScript rules only on `.ts` files).

---

## 2. Plan Mode — Don't Skip on Complex Tasks

Press **Shift+Tab** to cycle into Plan Mode before building. Non-negotiable for anything touching >3 files.

**Recommended workflow:**
1. **Explore** — Ask Claude to analyze existing code
2. **Plan** (Shift+Tab) — Let Claude research and propose an implementation plan
3. **Review** — Read the plan carefully. Catch problems before code is written
4. **Code** — Switch to auto-accept mode and let Claude execute

If things go sideways, re-plan rather than course-correcting mid-stream.

---

## 3. Context Management

Claude Code has a 200K token window; performance degrades as it fills.

| Command | Purpose |
|---------|---------|
| `/compact` | Compress conversation history. Do this at **~50% capacity**, not waiting for auto-compact at 95% |
| `/clear` | Start fresh for a new, unrelated task |
| `/cost` | View token consumption by tool call |
| `/context` | Audit what's in your context window |

**Rule of thumb:** One task per session. Don't mix features.

---

## 4. Effective Prompting

### The Pattern

Start with **what** and **why**, not **how**:
- ❌ *"Create auth.ts using the jose library with a verifyToken function"*
- ✅ *"I need JWT verification for my API routes. Keep it simple."*

### Push for Quality

Don't accept the first solution:
- *"Prove to me this works"* — have Claude diff behavior between branches
- *"Knowing everything you know now, scrap this and implement the elegant solution"*
- *"Grill me on these changes and don't make a PR until I pass your test"*

### Prompting Tricks

| Technique | Benefit |
|---|---|
| **Use `@filepath`** | Skips Claude searching — pinpoints context |
| **Prefix `!` for bash** | `!git status` runs inline, output lands in conversation |
| **Raw data > interpretation** | Paste error logs or CI output and say "fix" |
| **`/btw` for side questions** | Ask quick questions without interrupting work |
| **Structured prompts** | Context + Action + Success criterion — cuts iterations ~2x |
| **Specify effort** | *"Think step by step, this is harder than it looks"* |

---

## 5. Hooks — Deterministic Automation

Hooks run shell commands at lifecycle events. Unlike CLAUDE.md (advisory), **hooks always execute**.

### Most Useful Hooks

| Hook Event | What It's For |
|---|---|
| `PostToolUse` (Write \| Edit) | Auto-format every file after change |
| `PreToolUse` | Block dangerous commands before they run |
| `PostCompact` | Re-inject critical instructions after compression |
| `Stop` | On long-running tasks, nudge Claude to keep going |

### Example — Auto-Format on Every Edit

```json
{
  "PostToolUse": [
    {
      "matcher": "Write|Edit",
      "hooks": [{ "type": "command", "command": "bun run format || true" }]
    }
  ]
}
```

---

## 6. Parallel Sessions

Spin up multiple Claude Code sessions in isolated git worktrees:
```
claude --worktree --tmux
```
Run 3–5 agents in parallel — each in its own branch. Toggle between them with the terminal multiplexer.

- **`/batch`** — Tell Claude about a migration; it fans work out to worktree agents automatically
- **`claude --tmux`** — Sessions in a terminal multiplexer

---

## 7. Subagents for Specialized Work

Define custom agents in `.claude/agents/<name>.md`:

```markdown
---
name: security-reviewer
model: opus
tools: Read, Grep, Glob
---
You are a senior security engineer. Review all changed files for:
- Injection vulnerabilities
- Hardcoded secrets
- Auth bypasses
- Supply chain risks
```

Reference anywhere: *"security-reviewer, review the last 5 commits"*

---

## 8. Keyboard Shortcuts

| Shortcut | Action |
|---|---|
| `Esc` | Stop Claude mid-action |
| `Esc`+`Esc` | Rewind to a previous checkpoint |
| `Shift+Tab` | Cycle permission modes (Normal → Auto-accept → Plan) |
| `Ctrl+S` | Stash your current prompt draft |
| `Ctrl+B` | Send a long command to background |
| `Ctrl+G` | Open the plan in your editor for direct editing |

---

## 9. Pitfalls to Avoid

| Pitfall | Fix |
|---|---|
| **Vague requests** | Target <100 lines of code per request — 91% acceptance rate |
| **Editing behind Claude's back** | Say "I modified X, re-read it" |
| **Approving diffs without reading** | Catch bugs before they land |
| **Long sessions for everything** | `/clear` between unrelated tasks |
| **Not allowing safe commands** | `/permissions` to pre-approve `npm test`, `git status`, etc. |
| **Auto-compact at 95%** | Manually `/compact` at 50% instead |

---

## 10. Cost Optimization

| Practice | Impact |
|---|---|
| Compact at 50% (not waiting for 95%) | ~2.3s faster avg response time |
| Structured over narrative prompts | ~30% fewer tokens |
| Opus for hard decisions, flash for easy tasks | Smart model tiering |
| `/cost` dashboard | Visibility into what costs |
| `.claudeignore` for generated/vendor dirs | Less noise in context |
