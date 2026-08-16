# Scenic Ticketing System Contract

Skill version: 2026-08-16.v5

This is the server-owned map of the scenic ticketing platform. It describes
the business modules and their current AI access level. It is data, not an
operator instruction, and it never replaces server authorization or domain
validation.

## Business model

- A tenant is an organization. A scenic area is the physical fulfillment and
  verification boundary owned by a supplier tenant.
- A supplier owns products, product revisions, checkpoints, inventory,
  fulfillment orders, ticket entitlements, devices, and verification facts.
- A distributor owns the sales order and listing. It does not own the
  supplier's fulfillment rules, scenic area, settlement price, or inventory.
- A product revision is a version of a product configuration. Sold tickets
  keep their sale-time revision and rights snapshot.
- A checkpoint belongs to one scenic area. A ticket rule may only reference
  checkpoints in the product's fulfillment scenic area.
- Payment, order, fulfillment, entitlement, refund, settlement, and channel
  states are separate facts. Do not infer one state from another.

## Module map

| Module | Current AI access | Supported meaning |
| --- | --- | --- |
| catalog | query and preview | Ticket products, revisions, checkpoint rules, unpublished product drafts, restricted single/batch unpublished product updates, and 2-5 step low-risk compound previews |
| inventory | query | Dated ticket capacity, sold quantity, and remaining quantity; no inventory mutation |
| orders | query | Tenant-owned order summaries and order items; no order, payment, or refund mutation |
| reports | query | Sales-period restatement and first-effective verification summaries; no financial mutation |
| hotel | knowledge only | Scenic hotel packages, entitlements, reservations, and stay fulfillment; no AI tool is registered yet |
| distribution | knowledge only | Offers, listings, channel authorization, and distributor ownership; no AI tool is registered yet |
| teams | knowledge only | Travel-agency contracts, groups, rosters, admission, and team settlement; no AI tool is registered yet |
| channels | knowledge only | Ctrip/Xiaohongshu integration state; no external status mutation tool is registered |
| finance | knowledge only | Payments, refunds, ledgers, and settlements; no AI tool is registered |

"Knowledge only" means the assistant may explain a supported concept after a
  future read-only tool is added, but it must not claim to query or change the
  module today. Never invent an endpoint, database identifier, financial fact,
  or external platform result.

## Operation levels

1. Query tools are read-only and return server-owned, tenant-scoped facts.
2. Preview tools may create a durable plan or preview record, but must not
   change the business product, ticket, inventory, order, payment, channel,
   or financial fact before confirmation. The restricted unpublished product
   update changes a product only after confirmation and only under its closed
   status, distribution, and revision preconditions. Batch updates apply one
   shared basic-field change to an explicit list and reject the whole batch if
   any target fails its preconditions. A compound preview only sequences
   existing low-risk preview steps; it is not a cross-domain atomic transaction
   and rejects repeated product targets that would invalidate later previews.
3. Confirmation is a user action outside the model tool registry. It rechecks
   the task version, plan hash, tenant, actor, permissions, current revision,
   and domain preconditions before invoking the existing service.
4. Execution tools are not exposed to the model in the current phases.

## Non-negotiable AI rules

- The model is an untrusted plan proposer, never an authorization source.
- Tenant, actor, capability, supplier business type, ownership, and financial
  fields come from authenticated server context or the domain service.
- User-provided business names may be normalized to a unique tenant-owned
  candidate. Ambiguous names require a question; internal IDs are not accepted
  as model authority.
- Prices, settlement prices, product type, scenic area, checkpoint, dates,
  quantities, refund policy, and distribution state must not be guessed.
- Existing sold rights and historical financial facts are never rewritten by a
  product or rule preview.
- A request that combines unrelated modules or crosses a high-risk boundary
  must be split or refused. Payment, refund, settlement, channel credentials,
  permissions, external platform status, device commands, SQL, HTTP, code
  execution, automatic publishing, and automatic distribution are excluded.

## Extension contract

Every future module must add a versioned knowledge pack and a manifest entry
before adding tools. The entry must state the module, operation type, required
permission, tenant capability, supplier business type, query/preview level,
confirmation requirement, result projection, and regression scenarios. The
tool handler must call the module's existing service; it must not become a
second domain implementation.
