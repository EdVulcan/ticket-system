# Ticket inventory read module

This module is query-only. It reports supplier-owned dated ticket capacity,
sold quantity, and remaining quantity. It never reserves, releases, sells, or
changes inventory.

- A product name is a display selector; the server resolves the tenant-owned
  product and scenic area.
- Inventory facts are scoped by the authenticated supplier tenant and joined
  to the product and scenic area owned by that tenant.
- `remaining` is calculated by the server as capacity minus sold, clamped to
  zero for display. `over_capacity` reports an inconsistent projection.
- Missing rows mean no dated inventory fact was configured; do not call that
  unlimited stock unless the product configuration is separately returned.
- The default window is the most recent 30 calendar days, with a maximum of
  93 days and a bounded result set.
