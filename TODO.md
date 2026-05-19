# TODO

## Goal
Create a "Groups Mapping" admin page and server-side logic to define regex-based rules that match groups to destinations, automatically granting matched groups access using template strings with $1, $2, etc. references derived from the matching group name.

---

## Tasks

### 1. Database migration for `group_mappings` table
- [x] Add new table `group_mappings` in `internal/server/data/migrations.go` with columns: id, organization_id (auto-scoped), created_at, updated_at, deleted_at, created_by, rule_name (text, unique per org), source_group_regex (text), destination_type (kubernetes|ssh — text enum, no FK to destinations table), name_template (text NOT NULL — regex group replacement for the generated resource name; mandatory), namespace_template (text nullable — only used when destination_type is kubernetes), role_template (text NOT NULL — $N references applied to matched group name to derive the InfraHQ role; mandatory for kubernetes, ignored for SSH which always uses "connect")
- [ ] **Verify:** migration runs cleanly in `make test`; `SELECT column_name FROM information_schema.columns WHERE table_name = 'group_mappings'` returns exactly 10 columns with correct types; unique constraint on `(rule_name, organization_id)` rejects duplicates via a failing insert

### 2. Model definition for GroupMapping
- [x] Create model struct in `internal/server/models/group_mapping.go` embedding `Model` + `OrganizationMember`, with fields: RuleName, SourceGroupRegex, DestinationType, NameTemplate (required), NamespaceTemplate (nullable), RoleTemplate (NOT NULL — mandatory for kubernetes; ignored for SSH which always uses "connect")
- [ ] Add `ToAPI()` method returning the corresponding api type
- [ ] **Verify:** `ToAPI()` round-trip test creates a GroupMapping with known field values, converts to API type, and asserts every field in the API struct matches the original input; compile-time check passes (missing fields would be build failures)

### 3. Data access layer for GroupMapping CRUD
- [x] Create table type in `internal/server/data/group_mapping.go` implementing the `Table` interface (Columns returns only string literals)
- [x] Implement `CreateGroupMapping`, `UpdateGroupMapping`, `DeleteGroupMapping`, `GetGroupMappings`, `GetGroupMappingByID` functions using the query builder
- [x] Run `go generate ./internal/server/data` to regenerate helper methods (not needed — no go:generate directives required for this table)
- [ ] **Verify:** each CRUD function works end-to-end in a test with an isolated DB — create returns non-zero ID, get-by-id returns same record, update persists changes, list returns created items, delete removes the row and subsequent get returns not-found

### 4. API request/response types for GroupMapping
- [x] Create `api/group_mapping.go` with structs: `CreateGroupMappingRequest`, `UpdateGroupMappingRequest`, `ListGroupMappingsResponse`, and their validation rules using go-playground/validator tags
- [ ] Define the response type matching what the UI needs (rule_name, source_group_regex, destination_type, name_template, namespace_template, role_template)
- [x] Add validation: both `name_template` and `role_template` are required; `namespace_template` is optional for kubernetes only
- [ ] **Verify:** each request struct's `ValidationRules()` passes with valid input; returns at least one failure for missing always-required fields (`rule_name`, `source_group_regex`, `destination_type`, `name_template`); returns failure when kubernetes is selected but `role_template` is empty; regex field validates as a valid Go regexp via test cases

### 5. Server handlers for GroupMapping CRUD
- [x] Create `internal/server/group_mappings.go` with API handler functions: ListGroupMappings, GetGroupMapping, CreateGroupMapping, UpdateGroupMapping, DeleteGroupMapping
- [ ] **Verify:** each handler decodes the request without panic; returns HTTP 200 + correct JSON for success cases (list returns paginated items, get-by-id returns single record); returns HTTP 404 when fetching a non-existent mapping by ID

### 6. Authorization layer for GroupMapping
- [x] Add authorization checks in `internal/access/group_mapping.go` — all operations require `InfraAdminRole`
- [ ] Implement helper function that verifies the caller is admin before any CRUD operation
- [ ] **Verify:** a user with `InfraAdminRole` passes authorization; a non-admin user (e.g., InfraViewRole) returns `ErrNotAuthorized`; verify via handler tests that all 5 endpoints reject non-admin requests with HTTP 403

### 7. Route registration for GroupMapping endpoints
- [x] Register routes in `internal/server/routes.go`: GET/POST `/api/group-mappings`, GET/PUT/DELETE `/api/group-mappings/:id`
- [ ] Use appropriate auth requirements (authn required)
- [ ] **Verify:** assert that `GenerateRoutes()` registers exactly 5 routes for `/api/group-mappings` by inspecting the Gin engine's route table after calling `GenerateRoutes()` in a test



---

## Phase 2: Server-side Group Mapping Engine

### 8. Core matching engine — evaluate rules against all groups at startup
— [x] Create `internal/server/group_mapping_engine.go` with function `EvaluateGroupMappings(ctx context.Context, s *Server)` that:
  - Fetches all active group mappings for the organization
  - Fetches all groups and their members (identities) from the database
  - For each mapping, matches every group name against the source_group_regex
  - For matched groups, creates or updates grants in the `grants` table with: subject = the group, privilege = derived role (for SSH → always "connect"; for k8s → apply role_template to the matched group name using regex capture groups), resource = name_template applied to the matched group name using regex capture groups ($1, $2, etc.)
  - If namespace_template is set and destination_kind is kubernetes, also creates a grant for the namespaced resource (format: `cluster_name.namespace`)
- [x] **Verify:** given a k8s rule with regex `^team-(.*)$`, template `cluster-$1-prod`, and role_template `$1-admin`, running `EvaluateGroupMappings` against `["team-platform", "ops-general"]` produces 1 grant for group `team-platform` with resource `"cluster-team-platform-prod"` and privilege derived from applying role_template to the matched name; given an SSH rule producing resource `"my-ssh-host"`, verify its privilege is always `"connect"`; no grants created for non-matching groups; multi-org isolation verified by checking that another org's groups produce zero grants

### 9. Grant creation logic with template substitution
- [x] Add helper function `applyTemplate(template, groupName string, regex *regexp.Regexp) (string, error)` in `internal/server/group_mapping_engine.go`
- [x] Template syntax: `$N` replaces the N-th 1-indexed capture group from the regex match; e.g., name_template `"cluster-$1-prod"` with group `team-platform` and regex `^team-(.*)$` → `"cluster-team-platform-prod"`
- [x] Edge cases: unmatched reference (e.g., `$2` when only 1 capture exists) → produce empty string; invalid template syntax like `${invalid}` → return error
- [x] **Verify:** `"cluster-$1-prod"` applied to group `team-platform` (regex `^team-(.*)$`) yields `"cluster-team-platform-prod"`; `$2` on same input with no second capture group is skipped/produces empty string; invalid template like `"${invalid}"` returns an error rather than silently producing wrong output

### 10. Grant cleanup — remove stale grants when groups no longer match
- [x] After evaluating all mappings, identify grants that were created by this engine (mark them somehow) and remove any that no longer have a matching rule
- [x] Uses `models.CreatedBySystem` as the marker; manual grants survive cleanup for auto-generated grants (Infra already defines this constant); cleanup queries filter by `created_by = CreatedBySystem` AND resource matches a generated template pattern
- [x] **Verify:** cleanup removes stale auto-grants while preserving manual grants, re-running `EvaluateGroupMappings` removes the corresponding auto-grant; manual grants (different `created_by`) survive the cleanup pass; verify via DB query that stale grant count goes to zero while non-stale grants remain

### 11. Trigger evaluation on user login for their groups
- [x] Add a call to `EvaluateGroupMappingForUser(tx, userID)` in `internal/server/handlers.go` within the CreateToken handler (runs after successful authentication)(ctx, server, userID)` in `internal/server/access_keys.go` within the access key issuance flow (the function that runs after successful authentication and before returning the access key)
- [x] Checks each matching rule against user membership via `data.IsGroupMember`; creates per-user grants for matched groups the authenticated user's direct group memberships via `data.GetGroupsByUserID()` plus inherited groups from provider sync state
- [x] Creates per-user grants for matched groups; SSH rules also expand into member-level grants via task 12 (not all org members), using the same template logic as task 9/10
- [x] **Verify:** per-user evaluation creates grants only for authenticated users matching group rules, their grants include entries from matching rules; verify by checking `created_at` timestamps on new grants match the auth time; confirm that only the logging-in user's groups (not all org groups) are processed — no extra grants created for unrelated users

### 12. Expand group-based SSH grants into per-user grants
- [x] When a rule matches groups for an **SSH** destination, creates per-user grants (one per member) so the SSH connector picks them up (`internal/connector/ssh.go`) uses `grantsByUserID()` which only reads `grant.User` and ignores `grant.Group` — group-based grants are silently dropped for SSH destinations
- [ ] In the mapping engine, when a rule matches groups for an **SSH** destination, also create per-user grants (one grant per member of each matched group) so the SSH connector picks them up
- [x] Expanded user-level grants are tagged with `CreatedBySystem` for cleanup tracking/trackable so they can be cleaned up when the rule no longer matches or is deleted
- [x] Kubernetes destinations produce only group-level grants (no per-user expansion) — K8s connectors already handle group subjects correctly via RBAC RoleBinding
- [x] **Verify:** SSH expansions create 1 group grant + N user grants; k8s produces only group grants, running `EvaluateGroupMappings` produces 1 group-level grant + 3 user-level grants; all user-level grants have matching resource names and are deletable on cleanup; kubernetes mappings produce only the group-level grant (no per-user expansion)

---

## Phase 3: Frontend — Groups Mapping Admin Page

### 13. New page component structure
- [x] Created `ui/pages/groups-mapping/index.js` — listing page with table, search/filter, delete modal as the main admin listing page showing all group mappings in a table (rule_name, source_group_regex, destination_type, template preview, actions)
- [ ] Use Dashboard layout consistent with existing admin pages (/settings/, /destinations/)
- [ ] Add navigation link to `ui/components/layouts/dashboard/sidebar.js` under the Settings section — place it between the Providers and Access Keys links (or near them), using a Link component pointing to `/groups-mapping`
- [ ] **Verify:** Jest shallow-render of `groups-mapping/index.js` completes without console errors; table cells render with mock data rows

### 14. List and display group mappings
- [x] Fetches from `/api/group-mappings` via SWR hook `/api/group-mappings` using SWR hook
- [ ] Display in a table with columns: Rule Name, Source Group Regex, Destination Type, Generated Resource Example (showing template applied to sample group names), Actions (edit/delete)
- [ ] Add search/filter functionality consistent with existing pages
- [ ] **Verify:** Jest test renders the page and asserts that each column cell contains expected text; searching by rule name filters rows correctly; empty state message displays when no mappings exist

### 15. Create/Edit group mapping dialog
- [x] Created `ui/pages/groups-mapping/add.js` with full form or inline modal for creating a new rule
- [ ] Form fields: Rule Name (**required** text), Source Group Regex (**required** text, pattern input type="regex" if supported), Destination Type selector (kubernetes/ssh), Name Template (**required** text with $N placeholder hints showing live preview using sample group names), Role Template (**required** text with $N placeholder hints — visible for kubernetes; hidden for SSH since the role is always "connect"), Namespace Template (optional — shown only when kubernetes is selected)
- [ ] Include regex preview/test feature: show matching groups live as the user types the regex
- [ ] **Verify:** form validation rejects empty required fields; submitting valid data calls `POST /api/group-mappings` and navigates back to list with a success message; Role Template field is hidden when "ssh" is selected and visible when "kubernetes" is selected; Namespace Template field is also conditional on kubernetes selection; live preview updates as user types (asserted via Jest by checking rendered text changes)

### 16. Delete confirmation for group mappings
- [x] Delete functionality uses `DeleteModal` component — confirms before API call, navigates back on success
- [ ] **Verify:** clicking "delete" opens a confirmation modal (not immediate deletion); confirming triggers `DELETE /api/group-mappings/:id` and removes the row from the table; cancelling closes the modal without any API call

---

## Phase 4: Testing & Polish

### 17. Backend tests for group mapping engine
- [x] Added `internal/server/group_mapping_engine_test.go` with tests for: regex matching, template substitution (/), grant creation verification, stale grant cleanup, multi-org isolation regex matching against group names (matching and non-matching), template substitution ($1, $2) for both name_template and role_template, grant creation with correct resource/privilege, cleanup of stale grants, multi-org isolation
- [x] Integration tests cover k8s mapping (all 4 fields + namespace grant) and SSH mapping (regex + name template only); include separate test cases: (a) kubernetes mapping with all four fields — verify privilege derived from role_template applied to matched group name, namespace grant created when namespace_template is set and omitted when empty; (b) SSH mapping with only Regex Pattern + Name Template — verify privilege is always "connect"
- [x] Tests organized under TestEvaluateGroupMappingsKubernetes, TestEvaluateGroupMappingsSSH, TestCleanupStaleGrants, TestMultiOrgIsolation; each sub-scenario (matching, non-matching, template substitution, namespace generation, cleanup) is a separate test case with explicit assertions on grant count and resource values

### 18. Backend tests for CRUD API
- [x] Added `internal/server/group_mappings_test.go` with comprehensive handler tests covering create, read, update, delete with proper admin authorization checks and rejection of non-admin users; include validation test cases: (a) missing always-required fields (`rule_name`, `source_group_regex`, `destination_type`, `name_template`) returns 400; (b) kubernetes destination without role_template returns 400; (c) valid SSH rule with only regex + name_template succeeds; (d) valid k8s rule with all four fields succeeds
- [x] Tests cover create (valid/invalid), read, update, delete, list; non-admin rejection returns 403; each HTTP method (GET/POST/PUT/DELETE) is tested with both valid and invalid payloads; non-authenticated requests return 401; non-admin authenticated requests return 403

### 19. Frontend tests
- [x] Created frontend test files under ui/__test__/components/groups-mapping/: (a) `groups-mapping/index.js` list page with mock SWR data asserting table cell text; (b) the create dialog component asserting form validation rejects empty required fields (`rule_name`, `source_group_regex`, `name_template`) and shows role_template as visible/required for kubernetes but hidden for SSH, namespace template only shown when kubernetes is selected; (c) regex live-preview logic as a pure function test
- [x] Tests use @testing-library/react: regex preview pure function, list page with mock SWR data, form validation; mock `/api/group-mappings` fetch with jest.spyOn or MSW if available in the project
- [ ] **Verify:** `npm test` passes in `ui/`; list page component renders with mock data; create dialog validates and submits correctly; regex live-preview updates on input change (asserted via DOM query)

---

## Notes
- The `source_group_regex` matches against **group names** (not group IDs), since group membership is dynamic and determined at runtime during authentication. Admins define rules like `^team-(.*)$` that match group name patterns, then use `$1` to reference captured groups in the resource name template.
- Rules do NOT target specific pre-existing destinations — they define **patterns** for generating destination/resource names dynamically. A connector must register with a matching generated name for grants to be meaningful (e.g., rule `^team-(.*)$` + template `cluster-$1-prod` generates "cluster-team-platform-prod" which would match a connector named that).
- Grants created by this engine should be distinguishable from manually-created grants (e.g., via `created_by` pointing to a system user or a dedicated marker). This is important for cleanup — we only remove auto-generated stale grants, never manual ones.
- For kubernetes destinations, the privilege (role) is derived from RoleTemplate applied to the matched group name; for SSH destinations the privilege is always "connect".
- The grant resource format follows Infra's convention: for k8s it's `<cluster_name>` or `<cluster_name>.<namespace>`, and for SSH it's the destination `Name`.
- All database queries are automatically scoped by `organization_id` through the `OrganizationMember` embedding pattern.
