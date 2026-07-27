# InfraHQ Developer Guide

This guide covers the development workflows using Make targets for building, publishing, and running development environments.

## Prerequisites

- **Go 1.24.0** or later
- **Docker** with buildx support
- **Minikube** (for Kubernetes-based development)
- **Helm** (for Kubernetes deployments)
- **kubectl** configured for your cluster
- **goreleaser** (for release artifact builds) - [Install Guide](https://goreleaser.com/install/)
- **jq** (for JSON processing in test data scripts)
- **openssl** (for secret generation)

## Configuration Variables

The Makefile uses several configurable variables that can be overridden:

| Variable | Default | Description |
|----------|---------|-------------|
| `TAG` | from `.release` | Version tag for images and releases. Set by `.make/oci.mk`, so change it in `.release` rather than here |
| `DOCKER_ENGINE` | `docker` | Container engine to use |
| `DOCKER_REGISTRY` | `registry.gitlab.com/ska-telescope/external/infra` | Container registry URL |
| `INFRA_ACCESS_KEY` | `06e294c1bb.f636105ae3142c1c896fe1f9` | Default admin access key |
| `INFRA_PASSWORD` | `Passw0rd1!Thing` | Default admin password |

Go build/lint/test settings live in `go.mk` as `GO_*` variables — run
`make long-help` to list them with their defaults.

You can override these by:
1. Setting environment variables
2. Creating a `PrivateRules.mak` file with your overrides

---

## Development testing TL;DR

Local testing of all components needs Docker for Postgres, Infra Server and Infra UI, and Minikube for Kubernetes Connector.

Once you have Docker and a Minikube instance running, use the following to launch the entire stack:

```bash
make dev-oci
make dev-connector
```

To tear it all down:

```bash
make un-dev-connector
make clean
```

## Building and Publishing Container Images and Release Artifacts

### Building Container Images

Build both the Infra server and UI container images locally:

```bash
make docker-build TAG=dev
```

This target:
1. Runs `go fmt` and `go vet` on the codebase
2. Builds the Infra server image from `images/infra/Dockerfile`: `$(DOCKER_REGISTRY)/infra:$(TAG)`
3. Builds the UI image from `images/ui/Dockerfile`: `$(DOCKER_REGISTRY)/ui:$(TAG)`

Both recipes live under `images/<name>/` and build from the **repository root**
context, which is what `oci.mk` requires for a multi-image project. This is why
the UI Dockerfile's `COPY` paths are prefixed with `ui/`.

To build the same way CI does:

```bash
make oci-build-all      # builds every image under images/
make oci-lint           # hadolint over both recipes
```

### Publishing Container Images

Publishing is done by the pipeline, not by hand. `oci-image-publish` runs on a
git tag and pushes to the SKA registry. There is no `make docker-push` any more.

### Building Release Artifacts with GoReleaser

#### Local Test Build

Test the release build locally without publishing:

```bash
make release-artefacts-local TAG=<0.21.n git tag>
```

This creates a snapshot build in the `dist/` directory for verification. Artifacts are built for:
- Linux (amd64, arm64)
- macOS (amd64, arm64)
- Windows (amd64)

Several dev targets (`k8s-connector-key`, `infra-ssh-connector-key`,
`infra-ssh-config`) consume `dist/infra_linux_amd64_v1/infra`, so run this
first if you are setting up the SSH connector locally.

### Complete Release Workflow

Version state lives in `.release` and is propagated by the SKA release targets.

```bash
# 1. Check where you are
make show-version

# 2. Bump the version in .release
make bump-patch-release        # or bump-minor-release / bump-major-release

# 3. Propagate the version into the Helm charts
make set-release

# 4. Commit, push, and merge the bump
git commit -am "release <version>" && git push

# 5. From main, tag it - this is what triggers publishing
make git-create-tag
make git-push-tag
```

Pushing the tag runs `oci-image-publish` and `helm-chart-publish`.

CLI binaries are still published by hand — there is no `raw-publish` stage yet:

```bash
make release-artefacts-local TAG=<0.21.n git tag>   # then upload dist/ manually
```

---

## Docker-Based Development Environment

The Docker-based development environment runs all services as local containers using host networking.

### Quick Start

Launch the complete Docker dev environment with a single command:

```bash
make dev-oci
```

This command:
1. Cleans up any existing containers
2. Generates required secrets and certificates
3. Starts PostgreSQL
4. Starts the Infra UI container
5. Starts the Infra server container
6. Creates test data (users, groups, destinations, grants)

At completion, it displays the admin password.

### Services and Ports

| Service | Port | Description |
|---------|------|-------------|
| PostgreSQL | 5432 | Database |
| Infra Server (HTTP) | 8080 | API endpoint |
| Infra Server (HTTPS) | 8443 | Secure API endpoint |
| Infra Server (Metrics) | 9090 | Prometheus metrics |
| Infra UI | 3000 | Web interface |

### Accessing the Environment

- **UI/API:** http://localhost:8080

**Default Credentials:**
- Username: `admin@local`
- Password: Check `internal/server/testdata/initial-admin-password-secret/password`

Or use the dev user:
- Username: `dev@local`
- Password: `password123!This`

### Cleanup

Remove all Docker development containers:

```bash
make clean-oci
```

### Running Tests with Docker PostgreSQL

After starting PostgreSQL with `make postgres`, set the connection string:

```bash
export POSTGRESQL_CONNECTION="host=localhost port=5432 user=infra dbname=infra password=infra"

# Run short tests
make test

# Run all tests including integration tests
make test-all
```

---

## Minikube-Based Development Environment

The Minikube environment deploys Infra using Helm charts into a local Kubernetes cluster.

### Prerequisites

1. **Install and start Minikube:**

   ```bash
   minikube start
   ```

2. **Enable the LoadBalancer service (for accessing services):**

   ```bash
   minikube tunnel
   ```

   Keep this running in a separate terminal.

### Quick Start

Deploy the complete Minikube dev environment:

```bash
make dev
```

This command:
1. Sets kubectl context to minikube
2. Builds and loads the Infra and UI images into Minikube
3. Creates the `infra` namespace and required secrets
4. Deploys the Infra server and UI using Helm
5. Creates test data

### Step-by-Step Setup

For more control over the deployment:

```bash
# 1. Set kubectl context to minikube
kubectl config use-context minikube

# 2. Build container images
make docker-build TAG=dev

# 3. Load images into Minikube
make load TAG=dev

# 4. Deploy the server
make dev/server TAG=dev

# 5. (Optional) Deploy the connector
make dev/connector TAG=dev

# 6. (Optional) Add test data
make dev-test-data
```

### Accessing the Environment

Get the service URLs:

```bash
# Get the Infra server URL
kubectl -n infra get service infra-server

# Get the UI URL
kubectl -n infra get service infra-server-ui
```

With `minikube tunnel` running, access via the LoadBalancer IPs.

### Working with Test Data

Create test data in the running Minikube environment:

```bash
make dev-test-data
```

This creates:
- Test user: `dev@example.com`
- Test group: `Example`
- Test destination: `production` (Kubernetes)
- Grant: `Example` group has `view` access to `production`

### Cleanup

Remove the Minikube deployment:

```bash
make un-dev
```

This uninstalls Helm releases and deletes the `infra` namespace.

---

## Additional Development Commands

### Code Quality

```bash
# Format Go code
make fmt

# Run Go vet
make vet

# Run linting (with auto-fix)
make lint
```

### Building the Binary

```bash
# Build the infra binary
make build
# or
make bin/infra
```

The binary is created at `bin/infra`.

### Updating Dependencies

```bash
# Update Go dependencies
make go-update

# Update npm dependencies
make npm-update
```

### Generating Documentation

```bash
# Generate OpenAPI specification
make docs/api/openapi3.json
```

### Version Information

```bash
# Show current version tag
make version
```
