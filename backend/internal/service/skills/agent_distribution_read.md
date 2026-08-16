# Distribution read module

This module is query-only for the authenticated tenant with an active
`distributor` capability. It helps a distributor inspect only its own
cooperation and sales-side facts. It is not a tool for acting as a supplier,
altering another tenant's data, or advancing a commercial workflow.

- `query_distribution_partners` returns the current distributor's supplier
  relationship status and level. It excludes account balance, contacts,
  private memos, credentials, and database identifiers.
- `query_distribution_products` returns only current sell-side listing names,
  retail prices, relationship/listing/offer states, supplier-authorized sales
  dates, and allowed channels. It never returns supplier settlement price,
  quota, reserved quantity, inventory, ticket rules, or source identifiers.
- `query_distribution_fulfillments` returns aggregate fulfillment progress for
  orders sold by the current distributor. It may show business order numbers,
  supplier and scenic-area names, status, and ticket counts; it never returns
  visitors, phone numbers, identity documents, ticket codes, or inventory.
- `query_distribution_settlements` returns the current distributor's own
  settlement-statement lifecycle and aggregate amounts. It never returns
  supplier pricing detail, payment proof, dispute content, individual
  adjustment records, or internal identifiers.

The server derives tenant scope from authentication for every query. A paused
partnership must not erase historical sales, fulfillment, or settlement facts,
but it cannot make an offer sellable. Use only business names and numbers
returned by a QueryResult; do not request, invent, or infer database IDs.

Never use this module to apply for cooperation, create or change an offer,
import or synchronize a listing, bind an order, reserve inventory, refund,
confirm or pay a settlement, adjust a dispute, configure a channel, or access
channel credentials. Those operations remain in their dedicated human
workflows and are deliberately not AI tools.
