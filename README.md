<div align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="https://user-images.githubusercontent.com/251292/179098559-75b53555-e389-40cc-b910-0e53521efad2.svg">
    <img alt="logo" src="https://user-images.githubusercontent.com/251292/179098561-eaa231c1-5757-40d7-9e5f-628e5d9c3e47.svg">
  </picture>
</div>

[Infra](https://infrahq.com) provides authentication and access management to servers, clusters, and databases.

## Getting Started

See the [quickstart guide](./docs/quickstart.md)

## Documentation

For documentation, see the [docs](./docs)

## Community

- [Community Forum](https://github.com/infrahq/infra/discussions) Best for: help with building, discussion about infrastructure access best practices.
- [GitHub Issues](https://github.com/infrahq/infra/issues) Best for: bugs and errors you encounter using Infra.

## Mapping Rules

Infra supports group mapping rules that automatically create access grants based on IDP group membership. When the last mapping rule is removed, provider-synced groups that don't match any rule are automatically cleaned up — they serve no access purpose and are safely removed. Only groups synced from an identity provider are affected; locally-created groups are always preserved. See `docs/mapping-rules.md` for full documentation.


## How to set up locally

The following workflow spins up the Infra server, the web UI, and (optionally) a Kubernetes connector for local testing.

### Prerequisites

- Docker (for the Postgres dependency and optional image builds)
- Go 1.21+ (for building the backend)
- Node.js 18+ and npm (for the UI)
- Helm + kubectl if you plan to run the connector in Minikube
- apt-get install gcc-multilib installed
### 1. Start Postgres

Infra uses Postgres for persistence. Launch the dev instance with:

```bash
make postgres
```

This starts a `postgres-dev` container bound to the credentials in `dev/server.yaml`.

### 2. Build and run the backend

Build the CLI/server binary and start it with the dev configuration:

```bash
make bin/infra
INFRA_SERVER_CONFIG_FILE=dev/server.yaml ./bin/infra server
```

The API and UI proxy now listen on `https://localhost:9443` (self‑signed cert). Default login credentials live in `dev/server.yaml`.

### 3. Run the web UI

In a second terminal:

```bash
cd ui
npm install
npm run dev
```

The frontend is available at http://localhost:3000 and proxies API calls to the backend above.

### 4. (Optional) Add a local Kubernetes destination

1. Start Minikube (or any test cluster). Ensure `kubectl config current-context` points to it.
2. Create a connector access key from the Infra UI (`Settings → Connector Keys`). Copy the value into `config.accessKey` inside `dev/connector-values.yaml`.
3. Build and load the connector image if you want local changes (example):
   ```bash
   eval "$(minikube docker-env)"
   docker build -t infra-connector:dev -f Dockerfile .
   ```
4. Install/upgrade the connector Helm release using the supplied values file:
   ```bash
   helm repo add infrahq https://infrahq.github.io/helm-charts
   helm upgrade --install infra infrahq/infra \
     --namespace infra --create-namespace \
     --wait -f dev/connector-values.yaml \
     --set image.repository=infra-connector,image.tag=dev
   ```

Once the connector pod is healthy, a new destination should appear under **Destinations** in the Infra UI.

---

## Group Mapping Rules

Group Mapping Rules automatically create access grants based on identity provider group membership. When a user belongs to an IDP group whose name matches a rule's regex pattern, Infra creates a grant using templated resource and role names.

### How It Works

1. An admin creates a mapping rule with a regex pattern (e.g., `^team-(.*)$`) and templates for the destination name and role
2. On every group sync, destination change (create/update/delete), or IDP event — server startup also triggers evaluation
3. Groups matching the regex get auto-grants created with `$N` capture group substitution
4. When a rule is deleted or changed, the engine cleans up stale auto-grants automatically

### Creating a Mapping Rule

Navigate to **Settings → Group Mapping Rules** in the Infra admin UI (admin access required). Each rule has:

| Field | Required | Description |
|-------|----------|-------------|
| **Rule Name** | Yes | Unique identifier within your organization |
| **Source Group Regex** | Yes | Go regex to match against IDP group names |
| **Destination Type** | Yes | `kubernetes` or `ssh` |
| **Name Template** | Yes | Template for the resource name (supports `$N` captures) |
| **Role Template** | For k8s | Template for the RBAC role (required for Kubernetes) |
| **Namespace Template Regex** | For k8s | Optional template with `$N` captures and/or wildcards (`.*`, etc.). After applying capture group substitution, the engine compiles the result as a Go regex against the target K8s destination's live namespace list (discovered by the connector), creating one grant per matching namespace. |

### Template Syntax

Templates use `$N` syntax to reference regex capture groups:
- `$1`, `$2`, etc. — 1-indexed capture group references
- Multi-digit references supported (`$10`, `$25`)
- `${...}` syntax is NOT supported

### Example

Given a rule with `^team-(.+)-(.+)$` and input group `team-platform-dev`:

| Template | Output |
|----------|--------|
| `cluster-$1` | `cluster-platform` (resource name) |
| `$1-admin` | `platform-admin` (RBAC role via Role Template) |

### Namespace Template Regex — Wildcard Expansion

When the **Namespace Template Regex** is set for a Kubernetes rule, it triggers dynamic namespace scoping:

1. The engine queries the target K8s destination's `Resources` field (managed namespaces discovered by the connector)
2. Compiles the template result as a Go regex pattern against each namespace
3. Creates one grant per matching namespace with resource = `<cluster>.<namespace>`

Example: `Namespace Template Regex: platform-.*` with namespaces `platform-apps`, `platform-ingress` creates two grants:
- `resource=cluster-platform.platform-apps`
- `resource=cluster-platform.platform-ingress`

### Engine Behavior

- **Async execution**: Evaluation runs in a background goroutine so API responses are never blocked by the engine
- **Triggered on**: group sync, IDP events, server startup, and all destination/group CRUD operations (create/update/delete)
- **Eval status visibility**: The Mapping Rules page shows a success/error banner with the timestamp of the last evaluation. The raw status is also available via `GET /api/mapping-rules/eval-status`.
- **Auto-grant safe**: Engine-created grants are marked `auto_grant=true` and are the only grants eligible for cleanup
- **Idempotent**: Running the engine multiple times does not create duplicate grants
- **Graceful degradation**: Invalid regex or template in one rule does not affect other rules
- **Concurrency-safe**: Uses PostgreSQL advisory locks to prevent race conditions during evaluation
- **Cross-org isolation**: Rules only affect their own organization
- **Manual grants preserved**: Grants created manually (via UI/API) are never touched by the engine

### Local Test Data

```bash
make create-mapping-rule
```

This creates a sample SSH mapping rule matching groups named `ssh-connect-{name}-infra` and mapping them to SSH host `{name}`.
