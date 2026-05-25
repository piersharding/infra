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
2. On every group sync, IDP event, and server startup, the engine evaluates all active rules against all groups
3. Groups matching the regex get auto-grants created with `$N` capture group substitution
4. When a rule is deleted or changed, the engine cleans up stale auto-grants automatically

### Creating a Mapping Rule

Navigate to **Settings → Mapping Rules** in the admin UI (admin access required). Each rule has:

| Field | Required | Description |
|-------|----------|-------------|
| **Rule Name** | Yes | Unique identifier within your organization |
| **Source Group Regex** | Yes | Go regex to match against IDP group names |
| **Destination Type** | Yes | `kubernetes` or `ssh` |
| **Name Template** | Yes | Template for the resource name (supports `$N` captures) |
| **Role Template** | For k8s | Template for the RBAC role (required for Kubernetes) |
| **Namespace Template** | For k8s | Optional Kubernetes namespace scoping |

### Template Syntax

Templates use `$N` syntax to reference regex capture groups:
- `$1`, `$2`, etc. — 1-indexed capture group references
- Multi-digit references supported (`$10`, `$25`)
- `${...}` syntax is NOT supported

### Example

Given a rule with `^team-(.+)-(.+)$` and input group `team-platform-dev`:

| Template | Output |
|----------|--------|
| `cluster-$1` | `cluster-platform` |
| `cluster-$1.$2` | `cluster-platform.dev` |
| `$1-$2-admin` | `platform-dev-admin` |

### Engine Behavior

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
