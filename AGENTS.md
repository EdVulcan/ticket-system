# Repository Agent Guidance

Before changing domain code in this repository, load:

- `.codex/skills/scenic-ticketing-platform/SKILL.md`
- `docs/platform-multitenancy-development-guide.md`

The skill and development baseline are the source of truth for tenant isolation, supplier fulfillment, distributor sales, travel-agency teams, channels, payments, tickets, inventory, and settlement. Treat the current code as an audited legacy state until the Phase 0 gates are closed.

Do not relax tenant checks, copy supplier fulfillment rules into distributor tenants, accept client-controlled source or settlement fields, or add new storefront/channel work before the documented P0 gates and cross-tenant tests pass.

## Luna-first Engineering Workflow

The scenic-ticketing skill and development baseline above remain the domain source of truth. The routing rules below choose an execution model; they never override tenant isolation, fulfillment ownership, financial correctness, fail-closed integration gates, or the repository's verification requirements.

Use GPT-5.6 Luna Max as the primary model for normal coding, analysis, testing, review, and task orchestration. Sol is an on-demand advisor, not the default supervisor.

### Automatic routing

Before substantial work, silently choose the cheapest route that preserves quality:

1. `LUNA_LOCAL`: Luna handles the task in the primary thread when requirements are clear or delegation overhead would exceed the work.
2. `LUNA_PARALLEL`: Luna delegates at least two genuinely independent packets to `luna_worker` when parallelism materially improves speed or protects the main context.
3. `SOL_ADVISED`: Luna delegates one explicit hard decision to `sol_advisor`, receives a plan or ruling, then returns implementation to Luna.

Do not call Sol merely because a task is long or touches many files. Size creates Luna packets; uncertainty, risk, and reasoning difficulty justify Sol.

### Sol escalation gate

Call `sol_advisor` only when at least one condition holds:

- requirements remain materially ambiguous or contradictory after targeted inspection;
- architecture, security, privacy, authentication, authorization, cryptography, payments, destructive migration, data integrity, distributed consistency, or breaking compatibility requires a decision;
- several plausible root causes remain after the cheapest discriminating checks;
- two evidence-based implementation attempts failed;
- final validation exposes an unresolved risk whose plausible failure cost is high.

Before calling Sol, provide one decision question, the collected evidence, constraints and non-negotiables, options considered, and the required return format: recommendation, rationale, risks, implementation constraints, and acceptance criteria.

Sol does not perform routine implementation. After its decision, Luna executes and validates the plan. Request Sol review at the end only when the final artifact still contains a high-risk judgment.

### Luna parallelism

Use `luna_worker` for independent implementation, tests, exploration, documentation, and mechanical changes only when parallelism materially helps. Parallelize only when:

- packets do not depend on each other's unfinished output;
- every packet has explicit scope and acceptance criteria;
- writable files are disjoint;
- one owner is assigned per writable file;
- the primary Luna thread can integrate and validate the results.

Do not spawn agents for trivial tasks. More agents consume more tokens and can increase coordination cost.

### Task packets and acceptance

Every delegated packet must include objective, context, in-scope and out-of-scope files, constraints, acceptance criteria, exact validation, expected return, and escalation conditions.

Workers must stop on ambiguity, unexpected interface or dependency changes, security or data-integrity impact, unavailable validation, material scope expansion, or two failed attempts.

The primary Luna thread owns integration and normal final acceptance. Inspect actual diffs and validation results; do not accept summaries alone. Sol owns only the difficult decision it was asked to make and any explicitly requested high-risk final review.

Never claim a model ran unless the agent activity or tool result identifies it. If a configured model is unavailable, report the limitation and use the best available safe route.
