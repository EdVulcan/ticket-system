# Reports read module

This module is query-only and returns server-owned report facts. It does not
rewrite reports, settle money, refund tickets, or change financial records.

- Sales summaries attribute gross sales and successful refunds to the original
  successful payment date, so a later refund restates the original period's
  net amount. The refund event and audit remain in the refund workflow.
- Verification summaries use the first effective successful verification of a
  ticket. A successfully refunded verification is excluded from current
  verification income while its reversal remains auditable.
- The server owns date parsing, tenant scope, period limits, and report
  formulas. The model must not infer a different accounting period.
- Sales amount fields are yuan; verification `income_cents` is integer fen,
  matching the formal report service.
- The default window is the most recent 30 calendar days and a query may cover
  at most 366 days.
