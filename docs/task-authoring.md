# pm: task authoring guidelines

READ THIS before `pm_add_task` and before restructuring tasks mid-session. The
next session will ONLY have what's in the task.

## Creating tasks (pm_add_task) - capture ALL relevant context

- **spec**: the current-truth document (goes into the Spec zone, editable in place by future sessions). This is where the structured context lives:
  - `## Description` - what we're doing, why, who requested it, business goal
  - `## Context` - what was decided in conversation: decisions, rejected alternatives ("EXCLUDED: X, because Y"), constraints, requirements
  - `## Acceptance Criteria` - what must be true for the task to be considered done. Only include criteria explicitly stated or clearly implied by the user ("must work on mobile", "page loads under 2s"). Never invent criteria the user didn't mention.
  - `## Open Questions` - unresolved items (future sessions remove these from the Spec as they get answered).
  - `## Next Steps` - concrete checkboxes of what to do
  - Relevant code snippets, error messages, stack traces - verbatim, not paraphrased
  - File paths + line numbers, API endpoints, DB tables - anything specific discussed
- **body**: the append-only Log zone - session notes / a dated "Session 1: gathered context" entry. Leave empty at creation if there's nothing historical yet; the Spec carries the substance.
- **brief**: set immediately at creation. One-paragraph cold-start context so the next session can begin without reading the full body.
- **links**: every URL from the conversation - tickets, designs, PRs, threads, docs pages
- **tags**: relevant labels for filtering
- **branch**: if discussed or obvious from context
- Do NOT create sparse tasks with just a title. If context exists in the conversation, it MUST go into the task.

## ONLY VERIFIED FACTS

The task is a record of the conversation, not a design doc. The management
session is a recorder, not an architect.

Always include if mentioned in conversation:
- The user's requirements and expectations ("must be lazy loading", "max 2s response")
- The user's tech decisions ("use Redis, not Memcached")
- Constraints and blockers ("can't change the API - mobile depends on it")
- Business context ("client needs this for the Friday demo", "compliance requirement")
- Error messages, logs, stack traces the user shared - verbatim
- URLs, ticket numbers, stakeholder names, dates
- Rejected approaches with the reason ("EXCLUDED: X, because Y")
- Code snippets or file paths the agent actually read/verified in this session

Never include:
- Speculative implementation plans or architecture suggestions the agent invented
- Assumed technical approaches not discussed ("use encoding/csv", "add a handler in routes.go")
- File paths, function names, or code patterns the agent hasn't actually verified
- High-level implementation outlines the agent generated on its own

If the user said "add CSV export" - the task says "add CSV export" plus the
requirements/context from the conversation, NOT how to implement it. Research is
the job of the session that picks up the task.

## Task quality guidelines

- Decisions: note rejected alternatives ("EXCLUDED: X, because Y")
- References: file paths + line numbers, commit hashes, PR numbers, stakeholder names + dates
- Cross-refs: link related tasks by ID (e.g. "split from app-1" -> app-2, app-3)
- Brief: keep it updated on "doing" tasks - sessions read the brief instead of the full body
- Operational tasks (OTA, deploy): 1 sentence of context "why" + a link to the parent task

## Task hygiene during investigation (when a task grows mid-session)

- **Title must reflect current scope.** When a task about one bug grows to 4 people and 3 root causes - update the title. The next session reads the title first.
- **Organize by root cause, not per person.** One person can appear in 3 tasks - that's OK. "Related tasks" and "People map" sections connect the context.
- **Required sections in tasks with multiple people/blockers:**
  - `## Related Tasks` - explicit cross-refs with dependency descriptions (not just IDs)
  - `## People Map` - table: person, status, blockers, which other tasks they appear in
  - `## Client Communication` - what was asked, when, in which message
- **Absorbing tasks:** when an old task becomes a subset of a new one - add a note "absorbed into orbit2-X" in the old task instead of deleting. Decision history stays.
- **Stale Next Steps:** when checkboxes are completed - check them off or replace the list with a dated update. Don't leave old checkboxes next to new ones.

## Project links convention

- `project.yaml` links = stable service URLs (issue-tracker base, monitoring, docs)
- task links = specific artifacts (ticket ACME-327, PR #42, a commit)
- Keys: `jira`, `azure-board`, `azure-repo`, `figma`, `sentry`, `*-docs`
