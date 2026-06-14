# My AI Assisted Development Workflow

> Plan with a capable model, implement with a fast model — a tiered approach to
> AI-assisted development that balances quality, speed, and cost.

## Overview

The key insight: **different phases of development benefit from different model
capabilities.** Architectural reasoning and design exploration benefit from a
slower, more capable model ("pro" tier). Mechanical implementation — translating
a concrete plan into code — is well-served by a faster, lighter model
("flash/compact" tier).

The boundary between phases is the **plan artifact**: a written document that
captures decisions so the fast model doesn't need to rediscover them.

## The Core Loop (Plan → Implement → Verify → Document)

```
  ┌──────────────────────────────────────────────────┐
  │                  Research / Context              │
  │   (read existing code, understand the problem)   │
  └──────────────────────┬───────────────────────────┘
                         │
                         ▼
  ┌──────────────────────────────────────────────────┐
  │              1. PLAN (pro model)                 │
  │   • Understand requirements                      │
  │   • Explore approaches, weigh trade-offs         │
  │   • Write a concrete plan artifact               │
  │   • Get approval / clarify ambiguities           │
  └──────────────────────┬───────────────────────────┘
                         │
                         ▼
  ┌──────────────────────────────────────────────────┐
  │              2. IMPLEMENT (flash model)          │
  │   • Read the plan artifact                       │
  │   • Create/modify files per spec                 │
  │   • Handle edge cases named in the pla           │
  │   • If roadblocked → loop back to Ste 1          │
  └──────────────────────┬───────────────────────────┘
                         │
                         ▼
  ┌──────────────────────────────────────────────────┐
  │              3. VERIFY (either model             │
  │   • Run tests / build / lint                     │
  │   • Review for correctness                       │
  │   • If issues found → loop back to Step 1 or 2   │
  └──────────────────────┬───────────────────────────┘
                         │
                         ▼
  ┌──────────────────────────────────────────────────┐
  │              4. DOCUMENT (any model)             │
  │   • Capture implementation details for AI/LLMs   │
  │   • Write REFERENCE.md with types, functions,    │
  │     data flow, and design rationale              │
  │   • Update CLAUDE.md if new patterns emerged     │
  │   • Skip for trivial changes; invest for complex │
  └──────────────────────┬───────────────────────────┘
                         │
                         ▼
                    Complete
```

Each iteration of the loop covers one **coherent unit of work** — a feature, a
refactor, a bug fix. Don't try to plan and implement everything at once.

---

## Step-by-step

### Step 0: Prepare context

Before planning, gather what the fast model would miss:

- Read relevant existing files (or have the model read them).
- Note conventions, gotchas, and patterns from CLAUDE.md.
- Understand what success looks like.

The plan artifact should link to or reference these so the flash model doesn't
need to re-read the whole codebase.

### Step 1: Plan with a capable model

Switch to a pro-tier model explicitly (e.g. `/model deepseek-v4-pro` or
equivalent for your tool).

**What to produce:**

A plan document that covers:

1. **Goal** — one sentence on what's being built or changed.
2. **Files affected** — exact paths, with what each file does.
3. **Data flow** — how data moves through the system (function signatures,
   types, return values).
4. **Key decisions** — why this approach over alternatives.
5. **Edge cases** — what the plan explicitly handles (and what it defers).
6. **Failure modes** — things that could go wrong and how to detect them.

**Format:**

Write the plan to a file (e.g. `.claude/plans/<feature-name>.md`) or keep it in
the conversation. A file is preferred because:

- The flash model can `/read` it.
- You can revise it incrementally without losing history.
- It serves as documentation for later reference.

**Review:**

Before switching models, re-read the plan as if you were the implementer. Are
there ambiguities? Missing details? This is the last chance to catch them
without paying the round-trip cost.

**Exit criteria:**
- A developer who hasn't thought about this feature could implement it from
  this document alone.
- Every file, function, and data type is named.
- All known edge cases are documented.

### Step 2: Implement with a fast model

Switch to a flash-tier model (`/model deepseek-v4-flash` or equivalent).

**How to prompt it:**

Feed the plan as context. A good prompt structure:

```
I have a plan for [feature]. Here it is:

[plan content]

Please implement it. Read any existing files you need, then create/modify
the files as specified. If anything in the plan is ambiguous or impossible,
stop and tell me — don't guess.
```

**What to watch for:**

- **Plan drift** — the flash model may make reasonable-seeming choices that
  deviate from the plan. If it does, correct it and point back to the plan.
- **Missing edge cases** — the flash model may handle the happy path but skip
  error handling the plan specified. Verify.
- **Speed** — the flash model will complete files quickly. Let it run; review
  after.

**Roadblocks:**

If the implementer gets stuck (plan ambiguity, unforeseen dependency, design
issue that wasn't considered), stop and loop back to Step 1. Don't try to
"power through" with the flash model — design decisions belong in the pro
phase.

### Step 3: Verify (any model)

Verification can be done with either model:

- **Pro model** — better for reviewing correctness, security, and design
  coherence. Use when the implementation is complex or the stakes are high.
- **Flash model** — fine for builds, lint checks, running tests, and
  surface-level review. Use for routine verification.

**What to check:**

1. Does the implementation match the plan?
2. Do tests pass / does the build succeed?
3. Are there any new edge cases the plan missed?
4. Is the code consistent with existing conventions (formatting, naming,
   comments, error handling)?

**Outcomes:**

| Check result | Action |
|---|---|
| Everything matches | Done. Commit. |
| Minor issues found | Fix with flash model, re-verify. |
| Design flaw or missing feature | Loop back to Step 1. Update the plan. |
| Plan was incomplete | Update the plan with what was learned, then re-implement. |

### Step 4: Document for future AI/LLM use (any model)

After verification passes, capture the implementation's structure so future
AI-assisted work can pick up without re-reading all the code.

**What to produce:**

A `REFERENCE.md` (or equivalent per-project) that covers:

1. **Purpose** — what this component/service does (one paragraph).
2. **Types & structs** — each exported type, its fields, and what it represents.
3. **Functions** — key functions, their signatures, parameters, return values.
   Group by file or module.
4. **Data flow** — how data moves through the system. Diagrams if helpful.
5. **Key constants & variables** — what they control and where they're set.
6. **Design rationale** — why certain decisions were made (links back to the
   plan). This prevents future refactors from accidentally undoing intentional
   trade-offs.
7. **Usage examples** — minimal working example of how to call/run the code.

**Tooling:**

- The `/my-code-documenter` skill in Claude Code can auto-generate this
  reference from your codebase.
- For Go services, it extracts struct definitions, function signatures,
  exported constants, and produces structured markdown.
- For Python/JS projects, the same principle applies — you can prompt the
  model to read the files and produce the reference.

**How to decide if it's worth it:**

| Scenario | Document? |
|---|---|
| New service or package | Yes — especially if others will maintain it |
| Non-trivial feature | Yes — saves future round-trips |
| Bug fix (< 50 lines) | No — overkill |
| Refactor with no API change | No — unless it introduced new patterns |
| New public API surface | Yes — this is the primary consumer doc |

**Where to save it:**

- **Service/package level** — `REFERENCE.md` at the package root (e.g.
  `cmd/httptrace/REFERENCE.md`, `cmd/foldermcp/REFERENCE.md`).
- **Project-wide** — a single large `REFERENCE.md` at the project root if the
  codebase is small.
- **Linked from CLAUDE.md** — add a line to `CLAUDE.md` so the AI assistant
  knows where to look.

**Maintenance:**

A REFERENCE.md that drifts from the code is worse than none. Follow these
rules:

- Update it in the same PR/commit as the code change (co-located with the
  diff).
- If automated (via `/my-code-documenter`), regenerate it periodically or on
  major changes.
- If handwritten, keep it short — one page is better than ten. Capture what
  isn't obvious from reading the code.

---

## When to use each model

| Task | Recommended model | Rationale |
|---|---|---|
| Understanding a new codebase | Pro | Needs broad context, reasoning about relationships |
| Designing architecture | Pro | Trade-off analysis, predicting future needs |
| Writing detailed specs | Pro | Precision, completeness, anticipating edge cases |
| Implementing from a spec | Flash | Mechanical translation — fast, cheap |
| Debugging (root cause) | Pro | Hypothesis generation, trace analysis |
| Debugging (fix application) | Flash | Fix is usually clear once root cause is known |
| Code review (deep) | Pro | Security, correctness, design review |
| Code review (surface) | Flash | Style, lint, formatting, obvious bugs |
| Refactoring (plan) | Pro | Dependency mapping, migration strategy |
| Refactoring (execute) | Flash | Mechanical find-and-replace, rename, restructure |
| Writing tests (plan) | Pro | What to test, boundary analysis |
| Writing tests (code) | Flash | Boilerplate test code from a clear spec |
| Documentation (generate) | Flash | Struct/type extraction from stable code is mechanical |
| Documentation (design rationale) | Pro | Needs understanding of trade-offs and history |
| Running builds/lint/tests | Flash | Pure execution, no reasoning needed |
| Writing REFERENCE.md for AI/LLM | Flash | Use `/my-code-documenter` skill or direct prompt |

---

## Cost & performance characteristics

| Phase | Model tier | Typical tokens | Speed | Cost |
|---|---|---|---|---|
| Plan (1–3 iterations) | Pro | 5K–30K output | Slower | Moderate |
| Implement (full feature) | Flash | 10K–100K+ output | Fast | Low |
| Verify | Either | 5K–20K output | Fast | Negligible |
| Document (REFERENCE.md) | Flash | 3K–15K output | Fast | Negligible |

The pro model does ~5–20% of the total work (by tokens) but handles the
high-value reasoning. The flash model does ~80–95% of the work cheaply. This is
the most cost-effective split for non-trivial tasks.

The document phase is the smallest and cheapest — it's a one-time read of the
codebase with no iteration. But it multiplies the value of every future AI
session by giving the model structured context upfront.

---

## Practical tips

### Model switching in Claude Code

```sh
/model deepseek-v4-pro        # switch to pro model for planning
# ... do planning work ...
/model deepseek-v4-flash      # switch to flash model for implementation
```

### Planning templates

Keep a lightweight template in `.claude/plans/template.md`:

```markdown
# Plan: [Feature Name]

## Goal

## Files affected
- `path/to/file.go` — what changes

## Data flow

## Key decisions

## Edge cases handled

## Open questions / deferred
```

### REFERENCE.md template

When documenting for AI/LLM consumption (use `/my-code-documenter` or create
manually):

```markdown
# [Package/Service Name]

## Purpose
<!-- One paragraph on what this does -->

## Types
<!-- | Type | Fields | Description | -->

## Functions
<!-- | Function | Signature | Params | Returns | Purpose | -->

## Key constants & variables
<!-- | Name | Value | Purpose | -->

## Data flow
<!-- Description or ASCII diagram -->

## Design rationale
<!-- Why certain decisions were made — links to plans -->

## Usage
<!-- Minimal working example -->
```

### Guarding against common pitfalls

| Pitfall | Prevention |
|---|---|
| Plan is too vague | Read it as if you're the implementer. Fill gaps. |
| Flash model ignores plan | Call it out immediately — point to the specific line. |
| Plan doesn't survive implementation | Treat the plan as living. Revise and re-approve. |
| Spending too long planning | Set a timebox. If you're writing more than ~200 lines of plan for a small task, start implementing. |
| Switching models too often | Do all planning in one batch. Don't dip back and forth mid-task. |

### When NOT to tier

For very simple tasks (typo fix, one-liner, known convention change), skip
planning entirely — just implement with whichever model is active. The overhead
of switching models and writing a plan doesn't pay for itself on tasks under
~50 lines of change.

---

## Related

- [CLAUDE.md](../CLAUDE.md) — project-level instructions for the AI assistant
