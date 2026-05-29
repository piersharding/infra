# Group Mapping Rules

Group mapping rules automatically create access grants based on identity provider group membership. When a user belongs to an IDP group whose name matches a rule's regex pattern, Infra creates a grant using templated resource and role names.

## Overview

Mapping rules let you define **declarative access policies** that sync with your organization's directory structure:

1. Define a regex pattern matching identity provider group names (e.g., `team-(.+)-(.+)`)
2. Provide templates for the destination name, namespace, and RBAC role using `$N` capture references
3. The engine evaluates rules on every IDP sync event and at server startup, creating or updating grants automatically

## Creating a Mapping Rule

Navigate to **Settings → Group Mapping Rules** in the Infra admin UI (admin access required). Each rule has:

| Field | Required | Description |
|---|---|---|
| **Rule Name** | Yes | Unique identifier for the rule within your organization |
| **Group Matching Regex** | Yes | A Go regular expression to match against IDP group names. The regex must compile successfully. |
| **Destination Type** | Yes | Either `kubernetes` or `ssh` — determines what kind of access is granted |
| **Name Template** | Yes | Template for the destination/resource name using `$N` capture references (see below) |
| **Role Template** | Yes, if Kubernetes | Template for the RBAC role. Only required when Destination Type is `kubernetes`. |
| **Namespace Template Regex** | No, if Kubernetes | Optional template that may include `$N` capture references and/or wildcard patterns (`*`, `.*`). After applying capture group substitution, the engine compiles the result as a Go regex pattern against the target K8s destination's live namespace list (stored in the connector's `Resources` field), creating one grant per match: resource = `<cluster>.<namespace>`. |

## Template Syntax

Templates use `$N` syntax to reference capture groups from the regex match:

- `$1`, `$2`, etc. — reference the 1st, 2nd, ... captured group (1-indexed)
- Multi-digit references are supported (`$10`, `$25`)
- Only bare `$N` is supported — `${...}` syntax is **not** allowed

### Examples

Given a rule with regex `team-(.+)-(.+)`:

| Template | Input: `team-platform-dev` | Output |
|---|---|---|
| `cluster-$1` | `team-platform-dev` | `cluster-platform` |
| `$1-\$2-cluster` | `team-platform-dev` | `platform-dev-cluster` |
| `ns:$1-team-$2` | `team-platform-dev` | `ns:platform-team-dev` |

Given a rule with regex `(ops\|infra)-(admin\|users)`:

| Template | Input: `ops-admins` | Output |
|---|---|---|
| `$1-\$2-access` | `ops-admins` | `ops-admin-access` |

## Destination Types

### Kubernetes Destinations

For **kubernetes** destination type, the engine creates grants that map IDP groups to RBAC roles on your configured Kubernetes clusters:

- A cluster-level grant is created using the Name Template as the resource
- If Role Template is set, it's applied to determine the RBAC role name; otherwise `view` is used as a safe default
- The built-in Kubernetes role **`admin`** (which only provides namespace-level permissions) is automatically translated to **`cluster-admin`** (full cluster-wide access), since mapping rules intended for admin groups typically expect full cluster privileges. This translation applies when the resolved Role Template value is exactly `admin`.
- If **Namespace Template Regex** is set (see below), namespace-scoped grants are created dynamically against the destination's live namespace list

#### Namespace Template Regex — Wildcard Expansion

When you set the Namespace Template Regex field, the engine performs **wildcard namespace expansion**:

1. First applies any `$N` capture group references in the template to produce a Go regex pattern
2. Queries the target K8s destination (matched by `NameTemplate` result) and reads its `Resources` field — a comma-separated list of namespace names managed by the connector
3. Compiles the template result as a Go regular expression against each namespace
4. For every matching namespace, creates a grant with resource = `<cluster>.<namespace>`
5. On re-evaluation (triggered by rule changes, destination updates, or IDP sync), stale grants for namespaces that no longer match are cleaned up automatically

This means you can use wildcard patterns to scope access across many namespaces without listing each one individually.

**Example: Namespace Template Regex with wildcards**

Given a K8s destination named `cluster-platform` with these managed namespaces:
```
platform-apps, platform-ingress, platform-monitoring, platform-logging
```

And a mapping rule:
```
Rule Name:        platform-access
Source Group Regex: team-platform-(.+)-(.+)
Destination Type: kubernetes
Name Template:    cluster-$1
Role Template:    $2-admin
Namespace Template Regex: platform-.*
```

With group `team-platform-prod`, this creates:
- Cluster grant: resource=`cluster-platform`, role=`prod-admin`
- Namespace grants (one per matching namespace):
  - resource=`cluster-platform.platform-apps`, role=`prod-admin`
  - resource=`cluster-platform.platform-ingress`, role=`prod-admin`
  - resource=`cluster-platform.platform-monitoring`, role=`prod-admin`
  - resource=`cluster-platform.platform-logging`, role=`prod-admin`

**Example: Namespace Template Regex with capture groups**

Given a K8s destination `cluster-data` with namespaces:
```
data-prod, data-staging, data-dev
```

And a mapping rule:
```
Rule Name:        data-access
Source Group Regex: team-(data)-(.+)
Destination Type: kubernetes
Name Template:    cluster-$1
Role Template:    $2-role
Namespace Template Regex: data-$2
```

With group `team-data-prod`, this creates:
- Cluster grant: resource=`cluster-data`, role=`prod-role`
- Namespace grants (matching `data-prod`):
  - resource=`cluster-data.data-prod`, role=`prod-role`

**Example: Exact namespace match**

If you only want to scope to a single specific namespace, use an exact pattern:
```
Namespace Template Regex: production
```
This creates exactly one grant per matching group — no wildcards needed.

#### How It Works Under the Hood

- The Namespace Template Regex result is **always treated as a Go regex pattern** (not a literal name)
- If the target destination doesn't exist or is disconnected, the engine logs a warning and continues — it does not fail the entire evaluation
- Only grants with `AutoGrant=true` are cleaned up on re-evaluation; manual grants are never touched
- The expansion happens **per-rule per-group**: each matching group produces its own set of namespace-scoped grants

### SSH Destinations

For **ssh** destination type, the engine creates grants with privilege `connect`:

Example rule:
```
Rule Name:        ssh-access
Source Group Regex: ops-(.+)-(.+)
Destination Type: ssh
Name Template:    $1-infra-$2
```

With group `ops-general-dev`, this creates a grant allowing SSH access to host `general-infra-dev`.

## Auto-Grant Lifecycle

Grants created by mapping rules are marked with `AutoGrant=true` and `CreatedBy=system`:

- **Creation**: When a rule matches an active IDP group, Infra creates or updates the corresponding grant
- **Updates**: If a mapping rule changes (regex updated, template modified), stale grants are automatically cleaned up and new ones created on the next evaluation cycle
- **Cleanup**: Only `AutoGrant=true` grants with `CreatedBy=system` can be removed by the engine. Manual grants (`AutoGrant=false`) and bootstrap grants are always preserved

## Permissions

| Role | Can View Rules | Can Create/Edit/Delete Rules |
|---|---|---|
| InfraAdminRole | ✅ | ✅ |
| InfraViewRole | ✅ | ❌ |
| Unauthenticated | ❌ | ❌ |

Mapping rules define access policies that affect group-level permissions. While **viewers** can see the list of active rules (for transparency), only **admins** can create, modify, or delete them to prevent unauthorized policy changes.

## Troubleshooting

### Regex doesn't match expected groups

- Verify your regex compiles: Go's `regexp` package syntax is used
- Check group names in your IDP — the rule matches against the **exact group name** as stored in Infra's directory
- Test with the live preview in the admin UI, which shows which sample groups would match

### Template produces unexpected output

- `$N` references are 1-indexed (not 0-indexed like JavaScript)
- Use the live preview to see what your template produces for a given group name
- `$0` is **invalid** — capture references must be ≥ 1

### Namespace Template Regex creates too many grants

- The Namespace Template Regex result is compiled as a Go regex — verify it against your namespace list
- Use `platform-apps` instead of `.*` if you only need one specific namespace
- Check the destination's managed namespaces in **Settings → Destinations**

### Grants not being created

- Ensure the IDP sync has run since creating/modifying the rule
- Check server logs for warnings about invalid regex or failed template evaluation
- Verify the destination (Kubernetes cluster / SSH host) exists and is reachable in Infra's configuration
