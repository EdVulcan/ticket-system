# Scenic Ticketing Runtime Skill: Owned Product Updates

This skill covers one supplier-owned ticket product. It is a preview-only
operation; the server rechecks the product before confirmation and then uses
the normal product update transaction. A product may be online or marked
distributable and still be edited by its owning supplier. A distributor copy
is not supplier-owned and is rejected.

## Allowed fields

The request must identify the exact current-tenant product by `product_name`.
`changes` may contain only explicit values for:

- name, retail price, supplier settlement price;
- validity type/days/start/end dates;
- code mode, stock type/daily stock;
- real-name requirement and phone/ID purchase limits;
- refund type/rule, tags, and local gate voice code.

Missing values are not guessed. An empty string is an explicit request to clear
an optional text/date field where the domain permits it.

## Closed fields

This operation cannot change product type, scenic area, online/offline status,
distribution authorization, channel mapping, inventory reservations, payment,
refund execution, settlement, or checkpoint/rule-group membership. Use the
existing catalog rule preview for checkpoint rules. Distributed listings are
rejected even if their name is known.

## Preview and confirmation

The preview shows server-loaded before/after product facts and the changed
field labels. Confirmation locks the current product revision, requires the
product to remain the same tenant-owned product, and creates the next
`ProductRevision` through `ProductService`. Sold ticket snapshots and existing
channel facts are not rewritten. A version, name, or ownership change requires
a new task; status and distribution authorization are immutable in this tool.

Never return product IDs, tenant IDs, revision IDs, SQL, or an execution claim
to the provider. The server owns target resolution and all protected fields.
