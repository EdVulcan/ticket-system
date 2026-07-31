# Repository Agent Guidance

Before changing domain code in this repository, load:

- `.codex/skills/scenic-ticketing-platform/SKILL.md`
- `docs/platform-multitenancy-development-guide.md`

The skill and development baseline are the source of truth for tenant isolation, supplier fulfillment, distributor sales, travel-agency teams, channels, payments, tickets, inventory, and settlement. Treat the current code as an audited legacy state until the Phase 0 gates are closed.

Do not relax tenant checks, copy supplier fulfillment rules into distributor tenants, accept client-controlled source or settlement fields, or add new storefront/channel work before the documented P0 gates and cross-tenant tests pass.
