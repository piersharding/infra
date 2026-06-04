# Group Mapping Rules — Developer Guide

Group mapping rules let you define **declarative access policies** that automatically create grants when an identity provider group matches a configurable regex pattern. This guide explains how the engine works, what behaviors to expect, and where to adjust its behavior for your deployment.

---

## How the Engine Works

### Evaluation Flow

```
1. Trigger fires (see "Evaluation Triggers" below)
2. EvaluateMappingRulesAsync(db, orgID) spawns a background goroutine
3. Inside: begins own DB txn → acquires advisory lock per org → iterates rules × groups
4. For each matching group: apply templates → createOrUpdateGrant or update existing grant
5. After all rules processed: cleanupStaleGrants removes orphaned auto-grants + orphaned IDP groups
6. Records success/error in evalStatusStore (in-memory, per org)
```

### Key Behavioral Guarantees

| Property | Detail |
|----------|--------|
| **Async** | All triggers spawn a background goroutine with its own DB transaction. API responses are not blocked by evaluation completion. |
| **Serialized per org** | `pg_try_advisory_xact_lock(orgID)` prevents concurrent evaluations for the same organization. The second caller simply skips if the first holds the lock. |
| **Idempotent** | Running it multiple times produces the same result — safe to trigger without worrying about duplicates. `createOrUpdateGrant` checks for existing grants before creating. |
| **Graceful degradation** | Invalid regexes or templates are silently skipped per-rule-per-group. One bad rule does not crash the engine. |
| **Auto-grant isolation** | Only grants with `AutoGrant=true` (set exclusively by this engine) are cleaned up. Manual grants (`AutoGrant=false`) and bootstrap grants are never touched. |

---

## Evaluation Triggers

The engine runs asynchronously whenever state changes that could affect which groups match rules:

### 1. Server Startup
Once per organization — ensures persisted mapping rules produce grants before the server accepts traffic. Bootstrap config rules are loaded **before** this at `loadConfig()` (line 248 of `server.go`), so they participate in the first run.

### 2. Mapping Rule CRUD Operations
| Operation | File / Line | Why it triggers evaluation |
|-----------|-------------|--------------------------|
| Create | `mapping_rules.go:101` | New rule can match already-existing groups → creates grants for them |
| Update | `mapping_rules.go:125` | Changed rules may now match different groups or produce different resource names |
| Delete (soft) | `mapping_rules.go:157` | Removes auto-grants produced by the deleted rule via cleanupStaleGrants |

### 3. Group Lifecycle Changes
| Operation | File / Line | Why it triggers evaluation |
|-----------|-------------|--------------------------|
| Create group | `groups.go:48` | Newly-created group may match existing rules → creates grants for it |
| Delete group | `groups.go:60` | Orphanizes auto-grants with that group as subject; cleanupStaleGrants removes them |

> **Note:** `UpdateUsersInGroup` does **NOT** trigger re-evaluation. Changing group membership affects *who* has access, not *which groups match rules*.

### 4. Destination Lifecycle Changes
| Operation | File / Line | Why it triggers evaluation |
|-----------|-------------|--------------------------|
| Create destination | `destinations.go:76` | New destination can match existing NameTemplate → creates grants (enables K8s namespace expansion) |
| Update destination | `destinations.go:106` | **K8s only** — triggered when the old and new `Resources` lists differ; updates namespace-scoped grants |
| Delete destination | `destinations.go:120` | Orphanizes all auto-grants referencing it; cleanupStaleGrants removes them |

### 5. Post-Login (IDP Sync)
After every successful login involving an identity provider callback (`handlers.go:194`). Catches any new groups created by the IDP that may now match existing mapping rules, ensuring access grants are generated promptly after a user's group membership changes in their identity provider.

---

## Template Engine

### `$N` Capture References
Templates use bare `$N` syntax to reference capture groups from the regex match:

- `$1`, `$2`, etc. — 1-indexed (not 0-indexed like JavaScript)
- Multi-digit references supported (`$10`, `$25`)
- Only bare `$N` is allowed — `${...}` syntax is **rejected with an error**
- References must be ≥ 1; `$0` is invalid

The engine handles multi-digit references by sorting them descending during replacement, so larger numbers don't corrupt smaller ones (e.g., `$1` won't match the leading "$1" in "$11").

### Template Types

| Field | Applies To | What It Does |
|-------|-----------|-------------|
| **NameTemplate** | All types | Transforms matched group name into a destination/resource name. Evaluated for both K8s and SSH rules. |
| **NamespaceTemplate** | Kubernetes only | Transformed via `$N` substitution, then compiled as a Go regex against the target cluster's `Resources` list (configured namespaces). Produces `<cluster>.<namespace>` resources per match. |
| **RoleTemplate** | Kubernetes only | Transforms group name into an RBAC role name. Applied through global role replacements before resolution. Falls back to `"view"` if not set. |

### Auto-Anchored Regex Matching

`SourceGroupRegex` is automatically wrapped with `^...$` on every Create/Update (`mapping_rules.go:50`, `anchorRegex`) to prevent partial substring matches. If the regex already starts or ends with an anchor, it's added only if missing — no duplication.

---

## Kubernetes Destinations

The engine creates grants that map IDP groups to RBAC roles on your configured clusters:

1. A **cluster-level grant** is created using NameTemplate as the resource
2. If RoleTemplate is set, its value determines the RBAC role; otherwise `view` (least-privileged valid RBAC role) is used
3. The built-in Kubernetes role **`admin`** is automatically translated to **`cluster-admin`** when the resolved Role Template equals exactly `"admin"` — mapping rules intended for admin groups typically expect full cluster privileges, not just namespace-level permissions
4. Some deployments may define organization-specific roles with special behavior (e.g., a role that grants both its own privilege and elevated access). Check your deployment's configuration to understand any such custom roles

### Namespace Expansion

When `NamespaceTemplate` is set:

1. NameTemplate produces the cluster name (resource)
2. NamespaceTemplate is applied via `$N` substitution → compiled as Go regex
3. Compiled pattern matched against every namespace in the K8s destination's `Resources` list
4. For each match, creates a namespaced grant with resource `<cluster>.<namespace>`
5. If cluster doesn't exist or is disconnected — logs warning and skips (graceful degradation)

**Example:** With a rule using `NamespaceTemplate: platform-.*`, the engine expands this into individual grants for every matching namespace on the target cluster, rather than creating one grant per literal string match.

### K8s RoleBindings Periodic Resync

The Kubernetes connector periodically reconciles RBAC bindings even when destination metadata hasn't changed — catching drift from namespace deletion + recreation cycles within a sync window:

- **Default interval:** 5 minutes (configurable via `Options.ReconcileInterval`)
- **File / Line:** `internal/connector/connector.go:421` (`syncDestination`)
- Only runs when the destination has at least one resource configured and is a K8s destination
- Calls `updateRoles()` which re-syncs both ClusterRoleBindings and RoleBindings from the current grant set

---

## SSH Destinations

For **ssh** destination type, the engine creates grants with privilege `connect`. The SSH connector then resolves these into local user accounts:

| Grant Type | Condition | Behavior |
|------------|-----------|----------|
| Per-user | `grant.User != 0` | Handled directly — a local user account is created with that grant. |
| Group-based | `grant.Group != 0` | Resolved via `GetUsersInGroup()` API call; each member gets an account on the SSH host mapped to their grant. |

Mapping rules targeting groups now produce access for all members of those groups over SSH — previously only explicit per-user grants had any effect.

---

## IDP Group Filtering & Orphan Cleanup

### filterIDPGroups (login-time filtering)
**File:** `internal/server/data/provideruser.go:309`  
**Triggered by:** Each call to `SyncProviderUser` during login OIDC callback and SCIM sync.

Before persisting any group rows from an IDP, the server filters incoming groups. A group passes if it meets **any** of these three criteria:

| Check | Condition | Purpose |
|-------|-----------|---------|
| 1 | Locally created (`created_by_provider IS NULL` or `0`) | Admin-created groups always survive — they're not managed by the IDP |
| 2 | Matches an active mapping rule's `SourceGroupRegex` (pre-compiled per-rule) | Groups needed for access control are preserved even if no grant exists yet |
| 3 | Referenced by any grant as their subject (`subject_kind = 2`) — including auto-grants | Groups used in at least one access assignment survive even without matching rules |

Groups failing all three checks are dropped and never persisted (logged at WARN level). This prevents unbounded accumulation of IDP-synced groups that serve no access-control purpose.

### cleanupOrphanedGroups (evaluation-time cleanup)
**File:** `internal/server/mapping_rule_engine.go:686`  
Called at the end of every mapping rule evaluation (`cleanupStaleGrants`).

Soft-deletes IDP-synced groups (`created_by_provider != 0`) that no longer match any active mapping rule and have zero non-auto-grant references as their subject. Prevents orphaned group rows from accumulating when mapping rules are changed or removed.

- Soft-deleted grants do not count — only active grants (`deleted_at IS NULL`) preserve a group
- When there are zero active mapping rules, all IDP-synced groups lacking an active grant reference become orphans and are removed on the next evaluation cycle
- Locally-created groups (created manually in the UI) are never deleted by this process

---

## Configuration & Tuning Knobs

### Bootstrap Config (`server.yaml`)
Mapping rules can be defined declaratively alongside users:

```yaml
mappingRules:
  - name: platform-admins
    sourceGroupRegex: 'platform-.*'
    destinationType: kubernetes
    nameTemplate: cluster-platform-prod
    namespaceTemplate: platform
    roleTemplate: platform-admin
```

**Key behaviors:**
- Rules are loaded during `loadConfig()` (server.go line 151), within the same transaction as user/bootstrap data — **before** startup evaluation runs, so they produce grants on first evaluation
- Upsert by name: if a rule with this `name` already exists in DB, it is updated in-place (preserving ID and creation timestamp). If no matching rule exists, a new one is created.
- Rules loaded from config are marked with `CreatedBy = system`, so they participate in the auto-grant lifecycle — stale grants cleaned up on re-evaluation
- Config validation runs at startup: invalid regex, missing required fields (nameTemplate for all types, roleTemplate for kubernetes), or invalid destinationType values cause server startup to fail with a descriptive error including the rule name
- **Multi-org limitation:** Bootstrap config loads rules into the default organization only. In multi-org deployments, each org's mapping rules must be managed via UI or API

### Global Role Replacements
**File:** `internal/server/mapping_rule_engine.go:26` (`SetRoleReplacements`)  
Loaded from `BootstrapConfig.MappingRulesConfig.RoleReplacements` at server startup (server.go line 253).

Maps one base role name to multiple output roles. During evaluation, the engine checks each rule's resolved Role Template against all replacements in order — first match wins. Roles not matched by any replacement pass through unchanged. Output roles are deduplicated while preserving insertion order.

```yaml
mappingRulesConfig:
  roleReplacements:
    - from: platform-admin
      to: [cluster-admin, admin]
    - from: developer
      to: [edit, view]
```

### K8s Connector Reconcile Interval
**File:** `internal/connector/connector.go:67` (`Options.ReconcileInterval`)  
Default: 5 minutes. Controls how often the Kubernetes connector re-syncs RBAC bindings to catch drift from namespace deletion + recreation cycles. Set to `0` or omit to use the default.

---

## API Reference for Developers

### Evaluation Status
Check the result of the last mapping rule engine evaluation per organization:

```
GET /api/mapping-rules/eval-status
Infra-Version: 0.x.y
Authorization: Bearer <access-key>
```

Response (requires `InfraAdminRole`):
```json
{
  "last_run_at": "2024-03-14T09:48:00Z",
  "success": true,
  "error": null
}
```

The UI displays this status as a banner after rule changes. If an evaluation fails (e.g., due to invalid regex in any active rule), the error is logged server-side and visible via this endpoint.

### Matched Grants per Rule
When listing mapping rules (`GET /api/mapping-rules`), each returned rule includes a `matchedGrants` field showing which groups matched that rule's regex during the last evaluation, along with their grant details (privilege, resource name, destination ID). Populated from an in-memory cache (`MRGrantsCache`) keyed by `(orgID, ruleID)`. Cleared per-org at the start of each `EvaluateMappingRules` call.

---

## Debugging & Troubleshooting

### Regex doesn't match expected groups
- Verify your regex compiles: Go's `regexp` package syntax is used
- Check group names in your IDP — the rule matches against the **exact group name** as stored in Infra's directory (case-sensitive)
- Use the "Try it" input field below the regex to test against any custom group name and see real-time matching feedback

### Template produces unexpected output
- `$N` references are 1-indexed (not 0-indexed like JavaScript)
- Use the live preview to see what your template produces for a given group name
- `$0` is **invalid** — capture references must be ≥ 1
- Multi-digit references (`$10`, `$25`) are sorted descending during replacement so larger numbers don't corrupt smaller ones

### Namespace Template Regex creates too many grants
- The Namespace Template Regex result is compiled as a Go regex against the cluster's `Resources` list — verify it matches your namespace naming convention
- Use an exact pattern like `production` instead of wildcards if you only need one specific namespace
- Check the destination's managed namespaces in **Settings → Destinations**

### Grants not being created
- Ensure the IDP sync has run since creating/modifying the rule (login triggers it)
- Check server logs for WARN-level messages about invalid regex or failed template evaluation — these are logged per-rule-per-group and don't block other rules
- Verify the destination (Kubernetes cluster / SSH host) exists and is reachable in Infra's configuration

### Evaluation status is failing
- The error message in `GET /api/mapping-rules/eval-status` tells you what went wrong during the last evaluation
- Common causes: invalid regex in any active rule, failed K8s destination lookup (cluster disconnected), or template application errors for a specific group-rule combination
