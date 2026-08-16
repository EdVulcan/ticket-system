# Scenic Ticketing Runtime Skill: Unpublished Product Updates

This skill covers one supplier-owned ticket product that is still offline and
has never been authorized for distribution. It is a preview-only operation;
the server rechecks the product before confirmation and then uses the normal
product update transaction.

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
existing catalog rule preview for checkpoint rules. Distributed listings and
online products are rejected even if their name is known.

## Preview and confirmation

The preview shows server-loaded before/after product facts and the changed
field labels. Confirmation locks the current product revision, requires it to
remain offline and non-distributable, and creates the next `ProductRevision`
through `ProductService`. Sold ticket snapshots and existing channel facts are
not rewritten. A version or status change requires a new task.

Never return product IDs, tenant IDs, revision IDs, SQL, or an execution claim
to the provider. The server owns target resolution and all protected fields.
