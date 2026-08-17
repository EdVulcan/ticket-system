# Scenic Ticketing Runtime Skill: Batch Rule Changes

This skill covers supplier-side batch changes to existing ticket checkpoint rules.

Only these operations are supported: `add_checkpoints`, `remove_checkpoints`, and `set_checkpoint_limit`. Use exact candidate product and checkpoint names. A phrase such as “所有飞车套票” is a bounded category: select and output the exact candidate names that contain that category, one operation target at a time. Do not expand to every product unless the user explicitly says all/全部票种 or all/全部门票. When the user explicitly asks to add/create a new rule group while adding a checkpoint, set `create_group=true`; keep `group_name` empty until the user provides the new group name, and only fill `group_max_total_check_in` when the user explicitly gives that limit. When adding a checkpoint to a product with multiple existing rule groups without asking for a new group, keep `create_group=false` and `group_name` empty until the user chooses an existing group; never guess.

Existing-product targets are expressed with the shared `target_scope` object:
`intent`, `name_terms`, `scenic_area_names`, `listing_status`,
`product_type`, `all_scenic_areas`, and task-local `candidate_refs`. Use the
scope to preserve a user category such as “成人票” instead of choosing one
exact name prematurely. A unique exact name remains a single target unless
the user explicitly asks for a batch; multiple scenic areas or duplicate names
must be clarified. Scope filters are user evidence, not defaults: do not add
“listed”, “online”, or a scenic area unless the user said it or already
accepted it in the task. The server filters the current tenant catalog and
returns `missing_fields` when the scope is ambiguous; it does not create a
preview until the scope is resolved.

The server creates a revision preview with before/after facts, locks the current product revision at confirmation, and preserves sold ticket snapshots. Rule deployment does not pause active distribution offers or create a new channel authorization. The model does not directly edit products, revisions, offers, listings, inventory or tickets.

When a user explicitly asks for multiple low-risk changes in one request, the
runtime may use `prepare_compound_preview` for 2-5 independent steps. Each
step is previewed by its owning service and confirmation executes the child
tasks in order. Do not put the same product in two steps: a prior revision
change would make the later preview stale, so the server rejects that plan and
asks the user to merge it into one rule operation.
