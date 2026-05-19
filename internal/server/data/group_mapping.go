package data

import (
	"fmt"

	"github.com/infrahq/infra/internal/server/data/querybuilder"
	"github.com/infrahq/infra/internal/server/models"
	"github.com/infrahq/infra/uid"
)

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

func CreateGroupMapping(tx WriteTxn, mapping *models.GroupMapping) error {
	if err := validateGroupMapping(mapping); err != nil {
		return err
	}
	mapping.OnInsert()
	setOrg(tx, mapping)
	return insert(tx, (*groupMappingsTable)(mapping))
}

func UpdateGroupMapping(tx WriteTxn, mapping *models.GroupMapping) error {
	if err := validateGroupMapping(mapping); err != nil {
		return err
	}
	mapping.OnUpdate()
	return update(tx, (*groupMappingsTable)(mapping))
}

// DeleteGroupMapping soft-deletes a group mapping by ID.
func DeleteGroupMapping(tx WriteTxn, id uid.ID) error {
	table := &groupMappingsTable{}
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

func GetGroupMapping(tx ReadTxn, opts GetGroupMappingOptions) (*models.GroupMapping, error) {
	table := &groupMappingsTable{}
	query := querybuilder.New("SELECT")
	query.B(columnsForSelect(table))
	query.B(", update_index")
	query.B("FROM group_mappings")
	query.B("WHERE deleted_at is null AND organization_id = ?")
	query.B("AND id = ?", tx.OrganizationID(), opts.ByID)

	fields := append(table.ScanFields(), &table.UpdateIndex)
	err := tx.QueryRow(query.String(), query.Args...).Scan(fields...)
	if err != nil {
		return nil, handleError(err)
	}
	return (*models.GroupMapping)(table), nil
}

// ListGroupMappingsOptions holds the options for listing group mappings.
type ListGroupMappingsOptions struct {
	Name string // filters by rule_name (contains match)

	Pagination *Pagination
}

func ListGroupMappings(tx ReadTxn, opts ListGroupMappingsOptions) ([]models.GroupMapping, error) {
	table := groupMappingsTable{}
	query := querybuilder.New("SELECT")
	if opts.Pagination != nil {
		query.B(", count(*) OVER()")
	}
	query.B(columnsForSelect(table))
	query.B("FROM group_mappings")
	query.B("WHERE deleted_at is null AND organization_id = ?")
	query.B(tx.OrganizationID())

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
