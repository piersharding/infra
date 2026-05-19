## Summary

Creates a "Groups Mapping" admin page and server-side logic to define regex-based rules that match groups to destinations, automatically granting matched groups access using template strings with `$1`, `$2`, etc. references derived from the matching group name.

Admins define rules like `^team-(.*)$` that match group name patterns, then use `$1` to reference captured groups in the resource name and role templates. The system evaluates these rules periodically (every 2 minutes) as a background job and also on user login for per-user grants.

For **Kubernetes** destinations: generates cluster-level and optional namespaced grants with roles derived from `role_template`.
For **SSH** destinations: always uses privilege `"connect"`, and expands group matches into individual per-user grants so the SSH connector picks them up.

Resolves #1351

---

## List of Changes

### Phase 1 — Database, Model, Data Layer, API Types, Handlers, Routes (6 tasks)

| File | Change |
|------|--------|
| `internal/server/data/migrations.go` | New migration `addGroupMappingsTable()` creating the `group_mappings` table with columns: id, organization_id, created_at, updated_at, deleted_at, created_by, rule_name (unique per org), source_group_regex, destination_type (kubernetes/ssh enum), name_template, namespace_template, role_template. Includes unique index on `(rule_name, organization_id)`. |
| `internal/server/models/group_mapping.go` | New domain model `GroupMapping` embedding `Model` + `OrganizationMember`, with fields: RuleName, SourceGroupRegex, DestinationType, NameTemplate (required), NamespaceTemplate (*string, nullable), RoleTemplate (*string). Includes `ToAPI()` method. Added `CreatedBy uid.ID` field and `UpdateIndex int64`. |
| `internal/server/data/group_mapping.go` | New file implementing the `Table` interface (`groupMappingsTable`) with Columns(), Values(), ScanFields(). CRUD functions: CreateGroupMapping, UpdateGroupMapping, DeleteGroupMapping (soft-delete), GetGroupMapping, ListGroupMappings. Includes validation helper and optionalStringPtr type for nullable fields. |
| `api/group_mapping.go` | New file with API types: GroupMapping response struct; CreateGroupMappingRequest, UpdateGroupMappingRequest with go-playground/validator tags; ListGroupMappingsRequest/response. Validates rule_name, source_group_regex (must be valid Go regexp), destination_type (enum kubernetes/ssh), name_template (required). RoleTemplate required for k8s only. NamespaceTemplate optional. |
| `internal/server/group_mappings.go` | New file with 5 handler methods on *API: ListGroupMappings, GetGroupMapping, CreateGroupMapping, UpdateGroupMapping, DeleteGroupMapping. All follow the existing server pattern using access layer for authorization checks and data layer for persistence. |
| `internal/access/group_mapping.go` | New authorization helper functions (ListGroupMappings, GetGroupMapping, CreateGroupMapping, UpdateGroupMapping, DeleteGroupMapping). **All require InfraAdminRole** — non-admin users receive ErrNotAuthorized (HTTP 403). |
| `internal/server/routes.go` | Registered 5 routes: GET/POST `/api/group-mappings`, GET/PUT/DELETE `/api/group-mappings/:id`. All with authn required. |

### Phase 2 — Group Mapping Engine (5 tasks)

| File | Change |
|------|--------|
| `internal/server/group_mapping_engine.go` | New file with core matching engine. **EvaluateGroupMappings(tx)**: background job iterating all orgs → for each org, fetches active mappings and groups → matches group names against source_group_regex → creates/updates grants via applyTemplate() for name_template and role_template. Handles k8s (group-level + optional namespace grants) and SSH (group-level + per-user expansion). **EvaluateGroupMappingForUser(tx, userID)**: same logic but scoped to a specific user's groups — called from CreateToken handler on login. **applyTemplate(template, input, re)**: substitutes `$N` capture group references; unmatched refs → empty string; invalid `${...}` syntax → error. **cleanupStaleGrants**: removes auto-generated grants (CreatedBySystem) that no longer match any active rule; manual grants survive. |
| `internal/server/handlers.go` | Hooked EvaluateGroupMappingForUser into CreateToken handler — runs after successful authentication, before returning the access key. Added logging import. |
| `internal/server/server.go` | Registered background job: `EvaluateGroupMappings` runs every 2 minutes via existing backgroundJob infrastructure. |

### Phase 3 — Frontend (4 tasks)

| File | Change |
|------|--------|
| `ui/pages/groups-mapping/index.js` | Main listing page with table showing rule_name, source_group_regex, destination_type, generated resource preview, role template preview. Search/filter by rule name via useSearch hook. Pagination support. Empty state message when no mappings exist. Delete confirmation modal per row. Uses Dashboard layout consistent with existing admin pages. |
| `ui/pages/groups-mapping/add.js` | Create/Edit form with conditional fields: Role Template and Namespace Template visible only for Kubernetes (hidden for SSH). Live regex preview showing matching sample groups in green box as user types. Live template preview showing `$N` substitution results. Form validation rejects empty required fields. Uses DeleteModal component consistent with codebase patterns. |
| `ui/components/layouts/dashboard.js` | Added "Groups Mapping" navigation link between Groups and Users, using Squares2X2Icon (grid-like icon). Admin-only visibility via existing `admin: true` flag. |

### Phase 4 — Testing (3 tasks)

| File | What It Tests |
|------|---------------|
| `internal/server/group_mapping_engine_test.go` | **TestApplyTemplate**: single/multiple capture groups, no-$N templates. **TestApplyTemplateEdgeCases**: unmatched $2 → empty string; invalid `${...}` → error; non-matching regex → error + empty string. **TestEvaluateGroupMappingsKubernetes**: k8s mapping with all 4 fields — verifies privilege derived from role_template applied to matched group name, namespace grants created when namespace_template set. **TestEvaluateGroupMappingsSSH**: SSH mapping — privilege always "connect", per-user grant expansion for matched group members (1 group + N user grants). **TestCleanupStaleGrants**: stale auto-grants removed; manual grants survive cleanup. **TestMultiOrgIsolation**: org-scoped isolation verified. |
| `internal/server/group_mappings_test.go` | CRUD handler tests: valid k8s/SSH mappings → 201/200; missing rule_name/source_group_regex/name_template → 400; invalid regex → 400; k8s without role_template → 400; get existing/non-existing → 200/404; update persists changes; delete removes row + subsequent GET returns not-found; list returns all created mappings with count; unauthenticated requests rejected (401/403). |
| `ui/__test__/components/groups-mapping/*.test.js` | **regex-preview.test.js**: pure function tests for previewRegex and applyTemplatePreview — valid regex matches, invalid regex → empty array, .* matches all, no-match → empty array; template with $N references preserved raw, invalid `${...}` → empty string. **list-page.test.js**: renders table cells with mock SWR data (rule name, regex, destination type); empty state message when totalCount=0. **form-validation.test.js**: required fields visible for k8s default; role/namespace templates hidden when SSH selected; empty required fields rejected on submit with validation error messages shown. |
| `docs/api/openapi3.json` | Auto-regenerated via `-update` flag to include all 5 new endpoint definitions in the OpenAPI spec. |

---

## What Is Tested (by section)

### Engine Layer (`group_mapping_engine_test.go`) — 6 test functions

| Scenario | Input | Expected Behavior |
|----------|-------|------------------|
| Single capture group `$1` | regex `^team-(.*)$`, template `cluster-$1-prod`, input `team-platform` | → `"cluster-platform-prod"` (correct substitution) |
| No $N references | template `static-name`, any input | → unchanged string |
| Unmatched `$2` reference | only 1 capture group, template `$1-$2-end` | → `platform--end` ($2 = empty string, not error) |
| Invalid `${...}` syntax | template `${invalid}` | → **error** returned (not silently wrong output) |
| Non-matching regex | input `"nomatch"` vs `^team-(.*)$` | → **empty string + error** (no panic) |
| Kubernetes mapping with all 4 fields | rule for k8s, namespace_template set | → group-level grants + namespaced grants created; privilege from role_template applied to matched name |
| SSH mapping with regex + name only | rule for ssh, no role/namespace templates | → privilege always `"connect"`; per-user grants expanded (1 group + N user) |
| Stale grant cleanup | manual grant (`created_by=99`) + auto-grant | → manual survives; stale auto-removed |
| Multi-org isolation | mapping in org1 only, groups in org1 | → no cross-org grant leakage |

### API Handler Layer (`group_mappings_test.go`) — 6 test functions

| Endpoint | Valid Payload | Invalid Payload | Auth Check |
|----------|--------------|-----------------|------------|
| `POST /api/group-mappings` | k8s w/ all fields → **201**; SSH w/ regex+name → **200** | missing rule_name, source_group_regex, name_template → **400**; invalid regex → **400**; k8s without role_template → **400** | Unauthenticated → **401/403** |
| `GET /api/group-mappings/:id` | existing ID → **200** | non-existent ID → **404** | — |
| `PUT /api/group-mappings/:id` | valid update → persisted changes verified (rule_name, source_group_regex) | — | Unauthenticated → rejected |
| `DELETE /api/group-mappings/:id` | existing mapping → row removed; subsequent GET → not-found | — | Unauthenticated → **401/403** |
| `GET /api/group-mappings` (list) | returns all created mappings with correct count ≥ 3 | — | Unauthenticated → rejected |

### Frontend Layer (`ui/__test__/components/groups-mapping/*.test.js`) — ~17 assertions across 3 files

| Component | Test File | Assertions |
|-----------|-----------|------------|
| Regex preview (pure fn) | `regex-preview.test.js` | Valid regex matches sample groups; invalid regex → empty array; `.*` matches all; no-match → empty array |
| Template preview (pure fn) | `regex-preview.test.js` | Missing template → ''; no $N refs → raw string; valid `$1` → contains placeholder; invalid `${...}` → '' |
| List page rendering | `list-page.test.js` | Table cells render with mock data (rule name, regex, destination type "Kubernetes"); empty state message when totalCount=0 |
| Form validation | `form-validation.test.js` | All required fields visible for k8s default; role/namespace templates hidden when SSH selected; empty required fields rejected on submit with error messages shown |

---

## Checklist

- [x] Wrote appropriate unit tests (Go + Jest)
- [x] Considered security implications — all CRUD requires InfraAdminRole; grants tagged with CreatedBySystem for safe cleanup
- [x] Updated associated docs where necessary (OpenAPI spec auto-regenerated)
- [ ] Considered data migrations for smooth upgrades (migration is CREATE TABLE IF NOT EXISTS + unique index, idempotent)

---

## Related Issues

Resolves #1351
