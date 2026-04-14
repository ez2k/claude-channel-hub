---
name: plan
description: Planning mode — inspect context, write a markdown plan into .omc/plans/, and do not execute the work. Use when the user wants to plan before implementing.
version: 1.0.0
author: Claude Channel Hub
license: MIT
metadata:
  tags: [planning, plan-mode, implementation, workflow]
  related_skills: [debugging]
---

# Plan Mode

Use this skill when the user wants a plan instead of execution.

## Core behavior

For this turn, you are planning only.

- Do not implement code.
- Do not edit project files except the plan markdown file.
- Do not run mutating commands, commit, push, or perform external actions.
- You may inspect the repo or other context with read-only commands/tools when needed.
- Your deliverable is a markdown plan saved under `.omc/plans/`.

## Output requirements

Write a markdown plan that is concrete and actionable.

Include, when relevant:
- Goal
- Current context / assumptions
- Proposed approach
- Step-by-step plan
- Files likely to change
- Tests / validation
- Risks, tradeoffs, and open questions

If the task is code-related, include exact file paths, likely test targets, and verification steps.

## Save location

Save the plan under:
- `.omc/plans/YYYY-MM-DD_HHMMSS-<slug>.md`

Use this path relative to the active working directory.

## Interaction style

- If the request is clear enough, write the plan directly.
- If the user just says `/plan` with no other context, infer the task from the current conversation.
- If it is genuinely underspecified, ask a brief clarifying question instead of guessing.
- After saving the plan, reply briefly with what you planned and the saved path.

## Plan template

```markdown
# Plan: [Title]

**Date:** YYYY-MM-DD
**Goal:** [One sentence]

## Context

[Current state, what exists, what the user is working with]

## Approach

[High-level strategy]

## Steps

1. [ ] Step one
2. [ ] Step two
3. [ ] Step three

## Files to Change

- `path/to/file.go` — reason
- `path/to/other.go` — reason

## Validation

- [ ] Test: [how to verify it works]
- [ ] Build: [command]

## Risks & Open Questions

- Risk: [description]
- Question: [what is unclear]
```
