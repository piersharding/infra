# Changelog

## [0.21.13] — Mapping Rules Engine Enhancements

### Added

- **Group mapping rules** (new feature) — Declarative access policies that automatically create grants when an identity provider group matches a configurable regex pattern. Fully documented in `docs/dev/mapping-rules.md`.
  - Inline add/edit dialog with unsaved-changes warning on the list page
  - Template preview ($N capture references) for NameTemplate and RoleTemplate, sorted descending so multi-digit refs don't corrupt smaller ones
  - Evaluation status banner on list page; raw status via `GET /api/mapping-rules/eval-status`
  - Per-rule grant count with hoverable tooltip showing group name, permission, and destination for each matched grant (new `MappingRuleGrant` struct in API response)
  - Namespace template expansion as regex pattern against K8s destination's `Resources` list, one grant per matching namespace
  - Global role replacements (`MappingRulesConfig.RoleReplacements`) — maps a single base role to multiple output roles at evaluation time via bootstrap config. Replaces previously hardcoded `admin → cluster-admin` translation.
  - IDP group filtering on login and SCIM sync (`filterIDPGroups`) — incoming groups filtered before persistence; only locally created, rule-matching, or grant-referenced groups are kept
  - Orphaned group cleanup on evaluation (`cleanupOrphanedGroups`) — soft-deletes IDP-synced groups that no longer match any active rule and have zero non-auto-grant references

### Changed

- **K8s RoleBindings periodic resync** (5 min default) — The Kubernetes connector now reconciles RBAC bindings every 5 minutes to catch drift from namespace deletion + recreation cycles within a sync window. Configurable via `Options.ReconcileInterval`.
- **SSH connectors resolve group-based grants** — SSH connectors now call `GetUsersInGroup()` for group-based grants (`grant.Group != 0`), creating local user accounts for all members of mapped groups instead of only per-user grants. Previously, mapping rules targeting groups had no effect over SSH.

### Fixed

- **Stale auto-grants cleaned up when mapping rules are deleted** — Deleting a rule now triggers evaluation which removes all auto-grants that were produced by the removed rule via `cleanupStaleGrants`.
- **K8s namespace-scoped grants updated on destination Resources change** — Updating a K8s destination's `Resources` list (namespaces) now re-evaluates mapping rules when old and new lists differ, creating or removing namespace-scoped grants accordingly. Previously this was not triggered.
- **Orphaned group rows prevented from accumulating** — IDP-synced groups that no longer serve an access-control purpose are cleaned up on every evaluation cycle instead of persisting indefinitely in the database.
- **SSH connector sets password expiration on managed users** — The SSH connector now runs `chage -M 3650` (~10 years) for all infra-managed local user accounts, both newly created and pre-existing. This prevents SSH login failures caused by host-enforced password expiry policies (e.g., 90-day rotation). The update is applied on every sync cycle so no manual intervention is needed.
