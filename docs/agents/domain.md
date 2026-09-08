# Shared domain context

This repository uses one shared context for backend, admin, POS, miniapp and device code. Existing domain documents remain authoritative; do not introduce a second glossary or copy the business rules here.

## Read before affected work

1. `.codex/skills/scenic-ticketing-platform/SKILL.md`.
2. `docs/platform-multitenancy-development-guide.md` for the affected domain.
3. `docs/current-stage-goal-alignment-audit-2026-07-31.md`, respecting its dated status updates rather than treating historical findings as current facts.
4. `docs/current-development-roadmap-2026-08-01.md` for current delivery scope and remaining work.
5. `docs/field-integration-readiness-checklist.md` before real payments, devices, vendor channels or production capacity work.

The project skill and baseline govern tenant isolation, ownership, payments, refunds, immutable sale-time rights and settlement. Routing and engineering workflow skills cannot relax them. Use their business vocabulary consistently in hypotheses, tests, issues and implementation.

## Optional future context and decisions

There is currently no root `CONTEXT.md`, `CONTEXT-MAP.md` or `docs/adr/`. Their absence is not a setup blocker. If introduced later, read relevant vocabulary and decisions alongside the authoritative documents above. Do not create empty replacements just to satisfy a skill.

Surface conflicts with existing decisions explicitly; never silently override domain invariants. If a genuine domain decision changes, update the baseline under the project's review and authorization requirements.
