# Triage labels

| Canonical role | GitHub label | Meaning |
| --- | --- | --- |
| needs-triage | needs-triage | Maintainer needs to evaluate |
| needs-info | needs-info | Waiting for reporter information |
| ready-for-agent | ready-for-agent | Fully specified, ready for an agent |
| ready-for-human | ready-for-human | Requires human implementation |
| wontfix | wontfix | Will not be actioned |

As checked on 2026-09-08, only `wontfix` among these five exists remotely. The user approved these names as configuration, not creation of remote labels. Before applying a missing label, check `gh label list` and obtain task authorization to create it; do not silently substitute unrelated labels or claim triage succeeded.
