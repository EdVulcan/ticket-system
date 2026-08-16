# Orders read module

This module is query-only. It may show tenant-owned sales order summaries and
order items, but it never creates, cancels, pays, refunds, or edits an order.

- The server scopes every query by the authenticated tenant.
- Use business order numbers, product names, dates, status, and channel as
  returned by the tool. Never request or invent database IDs.
- The result does not include visitor identity documents, payment credentials,
  internal ownership IDs, or channel secrets.
- Channel-account implementation identifiers are normalized to business names
  such as `ctrip`; never ask the operator for a `ctrip:<id>` value.
- An order status is not a payment, fulfillment, refund, or settlement proof;
  describe only the facts returned by the query.
- The default window is the most recent 30 calendar days. A query may cover at
  most 366 days and is limited to a bounded result set.
