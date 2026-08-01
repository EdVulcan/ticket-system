---
name: scenic-ticketing-platform
description: Project-specific guardrails for the multi-tenant scenic ticketing platform, including supplier fulfillment, distributor sales, travel-agency teams, channels, payments, tickets, inventory, and tenant isolation. Use when changing backend models, services, APIs, admin/POS flows, database migrations, or tests in this repository.
---

# Scenic Ticketing Platform

## Mandatory context

Before changing domain code, read [the development baseline](../../../docs/platform-multitenancy-development-guide.md) completely for the affected area, then read [the current goal-alignment audit](../../../docs/current-stage-goal-alignment-audit-2026-07-31.md). Treat the baseline as the target design, the audit as the current blocker list, and the code as an evolving legacy state. Keep detailed rules in those documents; do not duplicate them here.

The product is a platform for supplier scenic areas, distributors, and travel agencies. It is not a reproduction of Zhiyoubao. The current implementation is a modular Go monolith with SQLite, a Vue admin app, and a Wails v2 + Vue POS; Electron remains only as a migration fallback. Read `docs/current-development-roadmap-2026-08-01.md` for the active six-level delivery order and `docs/field-integration-readiness-checklist.md` before work involving real payments, devices, vendor channels, legacy data, or production capacity. Do not add channels or storefronts ahead of the current production-closure work.

## Non-negotiable invariants

- Derive tenant and ownership scope from authenticated server context. Never trust request-body tenant IDs or client-provided supplier/source/settlement fields.
- Keep `Tenant` (organization) separate from `ScenicArea` (physical fulfillment and verification boundary). A tenant may later own multiple scenic areas.
- A supplier owns products, product revisions, inventory, checkpoints, devices, fulfillment orders, and ticket entitlements. A distributor owns the sales order and listing, not the supplier's fulfillment rules.
- Represent distribution with agreement/offer/listing entities. Do not copy supplier products and rules into a distributor tenant as an authorization mechanism.
- Verify a ticket against its immutable fulfillment scenic area and the checkpoint/device scenic area. Never relax tenant checks to make distributor tickets pass.
- Snapshot the product revision and ticket rights at sale time. Later product edits, removal, or rule changes must not rewrite sold ticket rights.
- Separate payment, fulfillment, entitlement, refund, and settlement states. Use idempotent reservations, callbacks, retries, and releases.
- Use integer cents or a safe fixed-point amount for money and immutable ledger entries for balances, credit, freezes, refunds, and settlements.
- Platform cross-tenant operations require a dedicated platform scope, explicit target tenant, reason, and audit record; ordinary tenant APIs stay tenant-scoped.

## Development workflow

1. Map the change to its owner, seller, supplier, fulfillment scenic area, channel, payment, and settlement scopes before editing code.
2. Identify whether the change belongs to platform, identity, catalog, distribution, inventory, ordering, ticketing, channel, payment, finance, team, or audit. Keep writes inside that module's service boundary.
3. Make server-side ownership authoritative. Validate agreement status, product offer, product revision, scenic area, inventory date/slot, and channel permissions inside the transaction.
4. Define normal, retry, timeout, cancellation, refund, and process-restart behavior. Persist work that must survive a restart; SQLite task/outbox tables are preferred before adding Redis or a message broker.
5. Add negative tenant tests before positive happy-path tests. At minimum test scenic A, scenic B, distributor D, travel agency T, and platform scope.
6. Run relevant Go tests, race tests where concurrency is affected, frontend builds, and business-flow E2E tests. Do not call mock printing, mock card reading, or a UI success toast a production feature.
7. Update the development baseline when a domain decision, invariant, phase, or acceptance rule changes. Record migrations and compatibility behavior for existing orders, tickets, inventory, and money.

## Current P0 gates

Do not mark production-ready or start storefront/channel expansion until these are closed with migrations and automated acceptance tests:

- Pending provider payments cannot be expired as ordinary unpaid orders; timeout, active query, late callback, release, and process restart converge without losing a paid order.
- Tenant capability checks fail closed across admin, POS, legacy OTA, and independent channel routes; frozen tenants cannot continue through external credentials.
- Every new fulfillment product, checkpoint, device, ticket entitlement, and check-in has a nonzero scenic area; legacy zero values are migrated or quarantined, never treated as wildcards.
- Distributor sale of a supplier product creates a supplier-owned fulfillment order and supplier-owned ticket entitlement that verifies at the correct scenic area, while another scenic area rejects it.
- Suppliers create or approve version-bound product offers; distributors cannot create authoritative settlement prices or expand channel authorization.
- Clients cannot forge `SourceProductID`, `SourceTenantID`, fulfillment scenic area, settlement price, or channel authorization.
- Unpaid reservations expire and release inventory and any provisional funds exactly once.
- Refunds restore the correct ticket entitlement, inventory, cash/credit facts, supplier receivable, and channel state exactly once.
- Settlement lines are unique to fulfillment facts, allocate refunds to the correct item/entitlement, use contract snapshots, and require controlled confirmation/payment transitions.
- Sold ticket rights remain valid according to their sale-time snapshot after product edits or retirement.
- Payment notifications, active queries, client polling, retries, and process restarts converge idempotently.
- Completed/verified orders remain in sales facts and reports; refunds and settlements adjust immutable ledger facts.
- Travel-team admission requires a valid supplier fulfillment right and supplier-controlled device/operator action; roster state cannot manufacture admission facts.
- Real legacy-database fixtures prove migration price, fulfillment, inventory, ticket, scenic-area, and ledger invariants before release.

## Forbidden shortcuts

- Do not remove tenant predicates, expose a global database handle, or add a broad admin bypass to fix a cross-tenant failure.
- Do not add another copied product/rule table for a new channel or distributor.
- Do not add a single `tenant_type` if capability combinations are needed.
- Do not add an order status to hide separate payment, fulfillment, refund, or settlement state.
- Do not use the tenant OTA secret as every external channel's identity.
- Do not implement a feature only in the frontend or use localStorage as authorization.

## Primary code map

- Models: `backend/internal/model/models.go`, `distribution.go`, `finance.go`.
- Core workflows: `backend/internal/service/order_service.go`, `ticket_service.go`, `distribution_service.go`, `payment_service.go`.
- Boundaries: `backend/internal/router/router.go`, `backend/internal/api/ota_controller.go`, `backend/internal/middleware/ota_auth.go`.
- Admin/POS: `admin/src/App.vue`, `admin/src/router/index.ts`, `admin/src/views/DistributionView.vue`, `admin/src/views/OperationsView.vue`, `desktop/src/renderer/src/views/SalesView.vue`, `desktop/src/renderer/src/components/PaymentModal.vue`.
- Detailed target, risks, roadmap, test matrix, and open questions: `docs/platform-multitenancy-development-guide.md`.
- Active implementation order and acceptance scope: `docs/current-development-roadmap-2026-08-01.md`.
