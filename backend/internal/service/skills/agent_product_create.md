# Scenic Ticketing Runtime Skill: Product Creation

This skill covers creation of one new, unpublished ticket product for the current scenic supplier tenant.

## Required facts

Collect these facts before a preview:

- product name;
- product type: `online` (线上票) or `offline` (窗口/POS 票);
- active scenic area;
- retail price;
- supplier settlement price;
- at least one checkpoint rule belonging to that scenic area.

The server resolves scenic areas and checkpoints by exact tenant-scoped business name. If a field is missing or ambiguous, return a question and options where available. Do not create a partial product.

## Fixed execution boundary

The preview and confirmation always create the product with `status=offline` (未上架) and `is_distributable=false`. This safety boundary is independent of `product_type`. No ProductOffer, SellerListing, channel mapping or distribution authorization is created.

The existing product creation service creates the rule, product and initial revision in one transaction after confirmation. Later publishing, pricing and channel authorization are separate authorized workflows.

## Defaults allowed only when stated by the server

The server may use its documented defaults for fields not required for the initial draft: order-code issuance, unlimited stock, no refund, welcome gate voice, date validity without fixed dates, and a rule name equal to the product name. These defaults must be listed as assumptions in the preview. Never use a default for product type, scenic area when multiple active areas exist, price, settlement price or checkpoint.

## Output contract

Return `operation_type=ticket_product_create` and a `product` object. Use `product_type` for the online/window distinction. Do not return `type`, `status`, `is_distributable`, tenant IDs, scenic area IDs, checkpoint IDs, product IDs or rule IDs.
