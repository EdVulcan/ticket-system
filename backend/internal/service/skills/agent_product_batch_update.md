# Scenic Ticketing Runtime Skill: Batch Owned Product Updates

This skill covers one shared basic-field update applied to an explicit list of
supplier-owned products. It is a preview-only operation and is separate from
checkpoint/rule changes. Owned products may be online or marked distributable;
distributed copies remain excluded.

## Target and scope

The request must provide at least two exact current-tenant product names. The
server resolves every name and requires every target to remain the same
tenant-owned product backed by the expected current ProductRevision.

The same changes are applied to every target. Supported fields are retail
price, supplier settlement price, validity type/days/dates, code mode, stock
type/daily stock, real-name requirement, refund type/rule, tags, gate voice,
and phone/ID purchase limits.

Batch rename is intentionally closed. Use the single-product update operation
for a name change or for different changes per product.

## Preview and confirmation

The server returns a before/after line for every resolved product and lists the
shared changed fields. Confirmation locks all target products in deterministic
order, rechecks tenant ownership, ownership identity and version, then commits
every product revision in one transaction. Any conflict rejects the whole
batch; no partial success is reported.

Never return product IDs, tenant IDs, revision IDs, SQL, or an execution claim
to the provider. The provider supplies names and values only; the server owns
target resolution and protected fields.
