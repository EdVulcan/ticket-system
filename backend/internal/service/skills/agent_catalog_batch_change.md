# Scenic Ticketing Runtime Skill: Batch Rule Changes

This skill covers supplier-side batch changes to existing ticket checkpoint rules.

Only these operations are supported: `add_checkpoints`, `remove_checkpoints`, and `set_checkpoint_limit`. Use exact candidate product and checkpoint names. A phrase such as “所有飞车套票” is a bounded category: select and output the exact candidate names that contain that category, one operation target at a time. Do not expand to every product unless the user explicitly says all/全部票种 or all/全部门票. When adding a checkpoint to a product with multiple rule groups, keep `group_name` empty until the user chooses a group; never guess.

The server creates a revision preview with before/after facts, locks the current product revision at confirmation, and preserves sold ticket snapshots. Rule deployment does not pause active distribution offers or create a new channel authorization. The model does not directly edit products, revisions, offers, listings, inventory or tickets.
