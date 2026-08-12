---
name: scenic-ticketing-platform
description: Project-specific guardrails for the multi-tenant scenic ticketing platform, including supplier fulfillment, distributor sales, travel-agency teams, channels, payments, tickets, inventory, and tenant isolation. Use when changing backend models, services, APIs, admin/POS flows, database migrations, or tests in this repository.
---

# Scenic Ticketing Platform

## Mandatory context

Before changing domain code, read [the development baseline](../../../docs/platform-multitenancy-development-guide.md) completely for the affected area, then read [the current goal-alignment audit](../../../docs/current-stage-goal-alignment-audit-2026-07-31.md). Treat the baseline as the target design, the audit as the current blocker list, and the code as an evolving legacy state. Keep detailed rules in those documents; do not duplicate them here.

The product is a platform for supplier scenic areas, distributors, and travel agencies. It is not a reproduction of Zhiyoubao. The current implementation is a modular Go monolith using PostgreSQL exclusively, a Vue admin app, and a Wails v2 + Vue POS; SQLite and Electron are not supported runtime or test paths. Read `docs/current-development-roadmap-2026-08-01.md` for the active six-level delivery order and `docs/field-integration-readiness-checklist.md` before work involving real payments, devices, vendor channels, or production capacity. Do not add channels or storefronts ahead of the current production-closure work.

## Non-negotiable invariants

- Derive tenant and ownership scope from authenticated server context. Never trust request-body tenant IDs or client-provided supplier/source/settlement fields.
- Keep `Tenant` (organization) separate from `ScenicArea` (physical fulfillment and verification boundary). A tenant may later own multiple scenic areas.
- A supplier owns products, product revisions, inventory, checkpoints, devices, fulfillment orders, and ticket entitlements. A distributor owns the sales order and listing, not the supplier's fulfillment rules.
- A distributor-owned cross-supplier bundle is presentation and pricing composition only. Its immutable version must expand atomically into supplier-owned component inventory, fulfillment orders, ticket entitlements, settlement facts, and separate scenic-area ticket codes.
- Never create a universal bundle ticket that verifies across suppliers or scenic areas. Component refunds use the sale-time retail allocation and affect only that component's supplier facts; supplier term changes stop new bundle sales without changing sold rights.
- A verified ticket normally remains non-refundable. The fulfillment supplier tenant's initial administrator may exceptionally issue the ordinary refund for a ticket fulfilled by that supplier, including a distributor-owned sales order. Payment and distributor ledger changes remain scoped to the original sales tenant; a successful refund reverses the active verification fact for business reports while retaining hidden, linked audit metadata. Never grant this exception to ordinary admins, distributors, platform operators, or another supplier.
- Represent distribution with agreement/offer/listing entities. Do not copy supplier products and rules into a distributor tenant as an authorization mechanism.
- Verify a ticket against its immutable fulfillment scenic area and the checkpoint/device scenic area. Never relax tenant checks to make distributor tickets pass.
- Snapshot the product revision and ticket rights at sale time. Later product edits, removal, or rule changes must not rewrite sold ticket rights.
- The hotel module is lightweight scenic-hotel-package fulfillment coordination, not a hotel PMS. It may own package configuration, date-based allotment, reservation snapshots, settlement allocation, and audited status synchronization reported by hotel staff or an external PMS. It must not grow room assignment, room cards, housekeeping, night audit, hotel deposits, standalone hotel front-desk workflows, or a duplicate guest profile system unless the product scope is explicitly changed later.
- Scenic-hotel packages must explicitly distinguish purchase-time dates from post-purchase booking. Deferred packages create one entitlement per package unit; before booking they must not expose a usable ticket code or reserve dated ticket/room inventory. Booking, cancellation, rescheduling, refund, and expiry must update entitlement, ticket, dated ticket stock, consecutive room nights, and external booking status together.
- Separate payment, fulfillment, entitlement, refund, and settlement states. Use idempotent reservations, callbacks, retries, and releases.
- Use integer cents or a safe fixed-point amount for money and immutable ledger entries for balances, credit, freezes, refunds, and settlements.
- Supplier income and upstream/downstream settlement are based on the first effective verification of each ticket entitlement, not on order creation or payment. A successful refund restores prepaid cash or credit immediately. If the verification was already included in a historical statement, claim its reversal exactly once in a later reconciliation statement without rewriting the historical statement or moving funds a second time.
- Platform cross-tenant operations require a dedicated platform scope, explicit target tenant, reason, and audit record; ordinary tenant APIs stay tenant-scoped.
- Tenant-facing workflows use business names, order numbers, ticket codes, device names, and current work context. Never require staff to remember database IDs when the system can present a scoped selector. One business fact should have one clear maintenance entry; related projections may synchronize transactionally instead of forcing duplicate configuration.

## Development workflow

1. Map the change to its owner, seller, supplier, fulfillment scenic area, channel, payment, and settlement scopes before editing code.
2. Identify whether the change belongs to platform, identity, catalog, distribution, inventory, ordering, ticketing, channel, payment, finance, team, or audit. Keep writes inside that module's service boundary.
3. Make server-side ownership authoritative. Validate agreement status, product offer, product revision, scenic area, inventory date/slot, and channel permissions inside the transaction.
4. Walk the affected task from the operator's point of view. Prefer name-based selectors, useful defaults, inline context, and a single clear primary action; keep internal IDs and redundant configuration out of visible forms.
5. Define normal, retry, timeout, cancellation, refund, and process-restart behavior. Persist work that must survive a restart in PostgreSQL task/outbox tables before adding Redis or a message broker.
6. Add negative tenant tests before positive happy-path tests. At minimum test scenic A, scenic B, distributor D, travel agency T, and platform scope.
7. Run relevant Go tests, race tests where concurrency is affected, frontend builds, and business-flow E2E tests. Do not call mock printing, mock card reading, or a UI success toast a production feature.
8. Update the development baseline when a domain decision, invariant, phase, or acceptance rule changes. Record migrations and compatibility behavior for existing orders, tickets, inventory, and money.

## Current P0 gates

Do not mark production-ready or start storefront/channel expansion until these are closed with migrations and automated acceptance tests:

- Pending provider payments cannot be expired as ordinary unpaid orders; timeout, active query, late callback, release, and process restart converge without losing a paid order.
- Tenant capability checks fail closed across admin, POS, legacy OTA, and independent channel routes; frozen tenants cannot continue through external credentials.
- Every new fulfillment product, checkpoint, device, ticket entitlement, and check-in has a nonzero scenic area; legacy zero values are migrated or quarantined, never treated as wildcards.
- Distributor sale of a supplier product creates a supplier-owned fulfillment order and supplier-owned ticket entitlement that verifies at the correct scenic area, while another scenic area rejects it.
- Suppliers create or approve version-bound product offers; distributors cannot create authoritative settlement prices or expand channel authorization.
- Clients cannot forge `SourceProductID`, `SourceTenantID`, fulfillment scenic area, settlement price, or channel authorization.
- Unpaid reservations expire and release inventory and any provisional funds exactly once.
- Refunds restore the correct unused ticket entitlement, inventory, cash/credit facts, supplier receivable, and channel state exactly once. A consumed ticket refunded by the supplier initial administrator does not restore historical capacity and no longer contributes to verification revenue.
- Settlement lines claim effective verification and reversal facts exactly once, allocate amounts to the correct item/entitlement from sale-time snapshots, exclude team orders from ordinary distribution settlement, and require controlled confirmation/payment transitions.
- Sold ticket rights remain valid according to their sale-time snapshot after product edits or retirement.
- Payment notifications, active queries, client polling, retries, and process restarts converge idempotently.
- Completed/verified orders remain in sales facts and reports; refunds and settlements adjust immutable ledger facts.
- Travel-team admission requires a valid supplier fulfillment right and supplier-controlled device/operator action; roster state cannot manufacture admission facts.
- If legacy import is explicitly requested later, use real sanitized fixtures and prove price, fulfillment, inventory, ticket, scenic-area, and ledger invariants before importing; legacy import is not a release gate for a new deployment.

## Forbidden shortcuts

- Do not remove tenant predicates, expose a global database handle, or add a broad admin bypass to fix a cross-tenant failure.
- Do not add another copied product/rule table for a new channel or distributor.
- Do not add a single `tenant_type` if capability combinations are needed.
- Do not add an order status to hide separate payment, fulfillment, refund, or settlement state.
- Do not use the tenant OTA secret as every external channel's identity.
- Do not implement a feature only in the frontend or use localStorage as authorization.

## Primary code map

- Models: `backend/internal/model/models.go`, `bundle.go`, `distribution.go`, `finance.go`.
- Core workflows: `backend/internal/service/order_service.go`, `bundle_service.go`, `ticket_service.go`, `distribution_service.go`, `payment_service.go`.
- Boundaries: `backend/internal/router/router.go`, `backend/internal/api/ota_controller.go`, `backend/internal/middleware/ota_auth.go`.
- Admin/POS: `admin/src/App.vue`, `admin/src/router/index.ts`, `admin/src/views/DistributionView.vue`, `admin/src/views/OperationsView.vue`, `desktop/src/renderer/src/views/SalesView.vue`, `desktop/src/renderer/src/components/PaymentModal.vue`.
- Detailed target, risks, roadmap, test matrix, and open questions: `docs/platform-multitenancy-development-guide.md`.
- Active implementation order and acceptance scope: `docs/current-development-roadmap-2026-08-01.md`.
