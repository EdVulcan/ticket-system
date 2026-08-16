# Travel-team read module

This module is query-only. It returns server-owned facts for a travel agency
or a supplier that is a party to the same team relationship. It cannot create
or edit a contract, team, roster, admission batch, confirmation, settlement,
or account fact.

- `query_team_contracts` returns contract number, counterparty name, status,
  settlement days, credit limit, date range, and the number of priced items.
  It never returns internal IDs or individual contract product prices.
- `query_team_groups` returns group number/name, counterparty, visit date,
  expected count, lifecycle state, settlement state, admitted count, batch
  count, and confirmation summary. It never returns roster names, phones,
  identity documents, ticket codes, guide/driver contacts, device IDs, or
  operator identities.
  When no visit window is supplied, it covers the coming 30 calendar days;
  explicit windows may cover at most 366 days.
- `query_team_settlement_summary` returns the statement and group business
  numbers, counterparty, status, amount summary, due date, and completion
  timestamps. It never returns payment proofs, dispute text, adjustment
  reasons, or internal settlement identifiers.
- `query_team_account_summary` returns a relationship-level balance summary:
  active contract count, credit use, pending and paid amounts, and dispute
  count. It is not a payment instruction or settlement confirmation.
- The server scopes every query to the authenticated tenant being either the
  travel agency or fulfillment supplier. A distributor without travel-agency
  capability cannot use these tools merely because it sells tickets.
- Admission is a supplier-controlled fulfillment event. The assistant may
  report the aggregate returned by the server, but must never instruct,
  trigger, simulate, or claim entry.
- A settlement status is a state observation only. Never infer a payment,
  a supplier acknowledgement, or an entitlement from it.
