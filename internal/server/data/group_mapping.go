package data

import (
	"fmt"

	"github.com/infrahq/infra/internal/server/data/querybuilder"
	"github.com/infrahq/infra/internal/server/models"
	"github.com/infrahq/infra/uid"
)

// groupMappingsTable implements the Table interface for the "group_mappings" database table.
// It maps between the models.GroupMapping struct and SQL operations (insert, update, scan).
type groupMappingsTable models.GroupMapping

func (g groupMappingsTable) Table() string {
	return "group_mappings"
}

func (g groupMappingsTable) Columns() []string {
	return []string{"created_at", "created_by", "deleted_at", "destination_type", "id", "name_template", "namespace_template", "organization_id", "role_template", "rule_name", "source_group_regex", "updated_at"}
}

func (g groupMappingsTable) Values() []any {
	return []any{g.CreatedAt, g.CreatedBy, g.DeletedAt, string(g.DestinationType), g.ID, g.NameTemplate, optionalStringPtr(g.NamespaceTemplate), g.OrganizationID, optionalStringPtr(g.RoleTemplate), g.RuleName, g.SourceGroupRegex, g.UpdatedAt}
}

func (g *groupMappingsTable) ScanFields() []any {
	return []any{&g.CreatedAt, &g.CreatedBy, &g.DeletedAt, (*string)(&g.DestinationType), &g.ID, &g.NameTemplate, (**string)(&g.NamespaceTemplate), &g.OrganizationID, (**string)(&g.RoleTemplate), &g.RuleName, &g.SourceGroupRegex, &g.UpdatedAt}
}

// CreateGroupMapping validates and inserts a new group mapping record.
// Validation ensures rule_name, source_group_regex, destination_type, name_template are non-empty,
// and that role_template is provided for kubernetes destinations. OrganizationID is set from the transaction context.
func CreateGroupMapping(tx WriteTxn, mapping *models.GroupMapping) error {
	if err := validateGroupMapping(mapping); err != nil {
		return err
	}
	mapping.OnInsert()
	setOrg(tx, mapping)
	return insert(tx, (*groupMappingsTable)(mapping))
}

// UpdateGroupMapping validates and updates an existing group mapping record.
// Runs the same validation as Create to ensure consistency. The updated_at timestamp is set by OnUpdate().
func UpdateGroupMapping(tx WriteTxn, mapping *models.GroupMapping) error {
	if err := validateGroupMapping(mapping); err != nil {
		return err
	}
	mapping.OnUpdate()
	return update(tx, (*groupMappingsTable)(mapping))
}

// DeleteGroupMapping performs a soft-delete on a group mapping (sets deleted_at).
// Only deletes within the transaction's organization scope. Returns an error if no matching row is found.
func DeleteGroupMapping(tx WriteTxn, id uid.ID) error {
	query := querybuilder.New("UPDATE group_mappings SET")
	query.B("deleted_at = now(), updated_at = now()")
	query.B("WHERE deleted_at is null AND organization_id = ?")
	query.B("AND id = ?", tx.OrganizationID(), id)

	result, err := tx.Exec(query.String(), query.Args...)
	if err != nil {
		return handleError(err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("group mapping not found")
	}
	return nil
}

// GetGroupMappingOptions holds the options for fetching a single group mapping.
type GetGroupMappingOptions struct {
	ByID uid.ID
}

// GetGroupMapping fetches a single non-deleted group mapping within the transaction's org scope.
// Returns an UpdateIndex from pg_notify to support real-time invalidation (used with LISTEN/NOTIFY).
func GetGroupMapping(tx ReadTxn, opts GetGroupMappingOptions) (*models.GroupMapping, error) {
	table := &groupMappingsTable{}
	query := querybuilder.New("SELECT")
	query.B(columnsForSelect(table))
	query.B(", update_index")
	query.B("FROM group_mappings")
	query.B("WHERE deleted_at is null AND organization_id = ?")
	query.B("AND id = ?", tx.OrganizationID(), opts.ByID)

	var updateIndex int64
	fields := append(table.ScanFields(), &updateIndex)
	err := tx.QueryRow(query.String(), query.Args...).Scan(fields...)
	if err != nil {
		return nil, handleError(err)
	}
	mapping := (*models.GroupMapping)(table)
	mapping.UpdateIndex = updateIndex
	return mapping, nil
}

// ListGroupMappingsOptions holds the options for listing group mappings.
type ListGroupMappingsOptions struct {
	Name string // filters by rule_name (contains match)

	Pagination *Pagination
}

// ListGroupMappings returns all non-deleted group mappings in the transaction's org scope,
// optionally filtered by name and paginated. Results are ordered alphabetically by rule_name.
func ListGroupMappings(tx ReadTxn, opts ListGroupMappingsOptions) ([]models.GroupMapping, error) {
	table := groupMappingsTable{}
	query := querybuilder.New("SELECT")
	if opts.Pagination != nil {
		query.B(", count(*) OVER()")
	}
	query.B(columnsForSelect(table))
	query.B("FROM group_mappings")
	query.B("WHERE deleted_at is null AND organization_id = ?")
	query.B("AND organization_id = ?", tx.OrganizationID())

	if opts.Name != "" {
		query.B("AND rule_name ILIKE ?", "%"+opts.Name+"%")
	}

	query.B("ORDER BY rule_name")
	if opts.Pagination != nil {
		opts.Pagination.PaginateQuery(query)
	}

	rows, err := tx.Query(query.String(), query.Args...)
	if err != nil {
		return nil, handleError(err)
	}
	defer rows.Close()

	var mappings []models.GroupMapping
	for rows.Next() {
		table := groupMappingsTable{}
		fields := table.ScanFields()
		if opts.Pagination != nil {
			fields = append(fields, &opts.Pagination.TotalCount)
		}
		err := rows.Scan(fields...)
		if err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}
		mapping := (*models.GroupMapping)(&table)
		mappings = append(mappings, *mapping)
	}

	return mappings, nil
}

// validateGroupMapping enforces business rules on the model before DB persistence:
//   - rule_name and source_group_regex are required
//   - destination_type must be "kubernetes" or "ssh"
//   - name_template is required for all types
//   - role_template is required when destination_type is "kubernetes" (needed to determine RBAC role)
func validateGroupMapping(mapping *models.GroupMapping) error {
	if mapping.RuleName == "" {
		return fmt.Errorf("rule_name is required")
	}
	if mapping.SourceGroupRegex == "" {
		return fmt.Errorf("source_group_regex is required")
	}
	if mapping.DestinationType != "kubernetes" && mapping.DestinationType != "ssh" {
		return fmt.Errorf("destination_type must be 'kubernetes' or 'ssh'")
	}
	if mapping.NameTemplate == "" {
		return fmt.Errorf("name_template is required")
	}
	if mapping.DestinationType == "kubernetes" && (mapping.RoleTemplate == nil || *mapping.RoleTemplate == "") {
		return fmt.Errorf("role_template is required for kubernetes destinations")
	}
	return nil
}
