# Agent Guide: InfraHQ Codebase

This document provides essential information for agents working effectively in the InfraHQ repository.

## Project Overview

**InfraHQ** is an authentication and access management platform for servers, clusters, and databases. It provides:
- User authentication and authorization
- Access management to Kubernetes clusters, databases, and other destinations
- Provider integration (Google, Azure AD, OIDC, Okta)
- API-driven access control

**Key Technologies:**
- Go 1.24.0 (backend)
- Next.js 14.2.0 (frontend UI)
- PostgreSQL (database)
- Redis (caching)

## Repository Structure

### Core Directories

- **`/internal/server/`** - Main server implementation
- **`/api/`** - API definitions and client libraries
- **`/ui/`** - Next.js frontend application
- **`/internal/cmd/`** - CLI commands and client utilities
- **`/docs/`** - Documentation
- **`/test/`** - Integration tests

### Key Files

- **`main.go`** - Server entry point
- **`internal/server/server.go`** - Server implementation
- **`internal/server/routes.go`** - API route definitions
- **`internal/server/data/`** - Database layer and models
- **`ui/pages/`** - Next.js page components

## Development Commands

### Backend (Go)

```bash
# Build the CLI/server binary
make bin/infra

# Run tests
make test

# Run all tests (including slow tests)
make test-all

# Run linting
make lint

# Format code
make fmt

# Build Docker images
make docker-build

# Start Postgres for development
make postgres

# Start the server (requires postgres)
INFRA_SERVER_CONFIG_FILE=dev/server.yaml ./bin/infra server
```

### Frontend (Next.js/React)

```bash
cd ui/

# Install dependencies
npm install

# Run development server
npm run dev

# Run tests
npm test

# Run linting
npm run lint

# Build for production
npm run build

# Run format checks
npm run format
```

### CI/CD Workflows

- **`ci-core.yaml`** - Backend Go tests, linting, PostgreSQL compatibility
- **`ci-ui.yaml`** - Frontend tests, linting, build verification
- **`fuzz.yaml`** - Fuzzing tests for security
- **`cd-containers.yaml`** - Container image builds
- **`cd-binaries.yaml`** - Binary releases

## Code Organization

### API Architecture

- **RESTful API** with versioning support
- **OpenAPI 3.0** specification
- **Organization-based multi-tenancy**
- **JWT-based authentication**

**Key API Endpoints:**
- `/api/users` - User management
- `/api/grants` - Access grants
- `/api/providers` - Identity providers
- `/api/destinations` - Resources to access
- `/api/groups` - User groups
- `/api/access-keys` - API tokens

### Database Layer

- **PostgreSQL** with custom functions and triggers
- **Query builder** pattern with type-safe column references
- **Automatic table method generation** from structs
- **Migration system** for schema changes

**Key Patterns:**
- `Table` interface for schema definition
- `Insertable`, `Updatable`, `Selectable` interfaces
- Custom query builder with linting enforcement
- Generated methods for table operations

### Frontend Architecture

- **Next.js** with React
- **Tailwind CSS** for styling
- **SWR** for data fetching
- **TypeScript** support
- **Headless UI** components

**Key Components:**
- `/ui/pages/` - Page components
- `/ui/components/` - Reusable components
- `/ui/lib/` - Shared utilities

## Testing

### Backend Testing

```bash
# Unit tests
go test ./...

# Tests with race detection
go test -race ./...

# Integration tests (requires PostgreSQL)
POSTGRESQL_CONNECTION="host=localhost port=5432 user=infra dbname=infra password=infra" go test ./...

# Test specific package
go test ./internal/server/...
```

### Frontend Testing

```bash
# Run Jest tests
npm test

# Update test snapshots
npm test -- --update-golden
```

### Test Data

- **`/test/`** - Integration test setup
- **`/internal/testing/`** - Test utilities
- **Docker Compose** setup for integration tests

## Code Style and Conventions

### Go Conventions

- **Error handling**: Always check errors, use `fmt.Errorf` for wrapping
- **Logging**: Use `github.com/rs/zerolog`
- **Context**: Always use context for cancellation and timeouts
- **Validation**: Use `github.com/go-playground/validator/v10`
- **API Design**: Follow REST conventions, use snake_case for JSON

### Frontend Conventions

- **React**: Functional components with hooks
- **Styling**: Tailwind CSS classes
- **Data**: SWR for client-side data fetching
- **TypeScript**: Strong typing throughout

### Database Conventions

- **Generated Code**: Use `go generate` for table methods
- **Query Safety**: All queries use the safe query builder
- **Column References**: Only string literals in `Table.Columns()`
- **Generated Methods**: Rerun `go generate` after schema changes

## Important Gotchas

### Database Operations

1. **Generated Methods**: After changing table structs, run `go generate ./internal/server/data` to regenerate methods
2. **Query Builder**: All dynamic queries must use the safe query builder
3. **Column Safety**: `Table.Columns()` must only return string literals (enforced by linter)
4. **Organization Scoping**: Most queries are automatically scoped by organization ID

### API Versioning

- **Backward Compatibility**: Use versioned handlers for breaking changes
- **Migration Pattern**: Create copy of structs with version suffix
- **Header Required**: All API requests need `Infra-Version` header

### Authentication Flow

- **Multi-tenancy**: Organization-based scoping
- **Provider Integration**: OAuth2/OIDC flows
- **Access Keys**: JWT-based tokens with expiration
- **Device Flow**: OAuth2 device authorization grant

### Frontend Patterns

- **Server Components**: Next.js app router
- **Data Fetching**: SWR with proper error handling
- **Type Safety**: TypeScript for API types
- **Styling**: Tailwind classes, responsive design

## Security Considerations

- **Input Validation**: Always validate user input
- **SQL Safety**: Only use the provided query builder
- **XSS Protection**: Escape output in templates
- **CORS**: Proper origin handling
- **TLS**: Default secure connections

## Performance

- **Query Optimization**: Use indexes, avoid N+1 queries
- **Caching**: Redis for frequently accessed data
- **Pagination**: Always paginate list endpoints
- **Frontend**: Code splitting, image optimization

## Deployment

- **Docker**: Containerized deployment
- **Kubernetes**: Helm charts available
- **Environment Variables**: Configuration via env vars
- **Certificates**: Automatic TLS with Let's Encrypt

## Common Tasks

### Adding a New API Endpoint

1. Define request/response structs in `/api/`
2. Add handler in `/internal/server/`
3. Register route in `/internal/server/routes.go`
4. Add tests
5. Update OpenAPI spec if needed

### Adding a Database Table

1. Create struct implementing `Table` interface
2. Add `CREATE TABLE` SQL in `/internal/server/data/schema.sql`
3. Run `go generate ./internal/server/data`
4. Implement CRUD operations
5. Add tests

### Adding a Frontend Page

1. Create page component in `/ui/pages/`
2. Add routing if needed
3. Implement data fetching with SWR
4. Add tests

### Adding a CLI Command

1. Create command in `/internal/cmd/`
2. Register in `NewRootCmd()` in `/internal/cmd/cmd.go`
3. Add tests

This guide should help you navigate and contribute to the InfraHQ codebase effectively. Always refer to existing code for patterns and conventions.
