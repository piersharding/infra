package data

import (
	"fmt"
	"regexp"

	"github.com/infrahq/infra/internal/server/data/querybuilder"
	"github.com/infrahq/infra/internal/server/models"
	"github.com/infrahq/infra/uid"
)

// mappingRulesTable implements the Table interface for the "mapping_rules" database table.
// It maps between the models.MappingRule struct and SQL operations (insert, update, scan).
type mappingRulesTable models.MappingRule

func (g mappingRulesTable) Table() string {
	return "mapping_rules"
}

func (g mappingRulesTable) Columns() []string {
	return []string{"created_at", "created_by", "deleted_at", "destination_type", "id", "name_template", "namespace_template", "organization_id", "role_template", "rule_name", "source_group_regex", "updated_at"}
}

func (g mappingRulesTable) Values() []any {
	return []any{g.CreatedAt, g.CreatedBy, g.DeletedAt, string(g.DestinationType), g.ID, g.NameTemplate, optionalStringPtr(g.NamespaceTemplate), g.OrganizationID, optionalStringPtr(g.RoleTemplate), g.RuleName, g.SourceGroupRegex, g.UpdatedAt}
}

func (g *mappingRulesTable) ScanFields() []any {
	return []any{&g.CreatedAt, &g.CreatedBy, &g.DeletedAt, (*string)(&g.DestinationType), &g.ID, &g.NameTemplate, (**string)(&g.NamespaceTemplate), &g.OrganizationID, (**string)(&g.RoleTemplate), &g.RuleName, &g.SourceGroupRegex, &g.UpdatedAt}
}

// CreateMappingRule validates and inserts a new mapping rule record.
// Validation ensures rule_name, source_group_regex, destination_type, name_template are non-empty,
// and that role_template is provided for kubernetes destinations. OrganizationID is set from the transaction context.
func CreateMappingRule(tx WriteTxn, mapping *models.MappingRule) error {
	if err := validateMappingRule(mapping); err != nil {
		return err
	}
	mapping.OnInsert()
	setOrg(tx, mapping)
	return insert(tx, (*mappingRulesTable)(mapping))
}

// UpdateMappingRule validates and updates an existing mapping rule record.
// Runs the same validation as Create to ensure consistency. The updated_at timestamp is set by OnUpdate().
func UpdateMappingRule(tx WriteTxn, mapping *models.MappingRule) error {
	if err := validateMappingRule(mapping); err != nil {
		return err
	}
	mapping.OnUpdate()
	return update(tx, (*mappingRulesTable)(mapping))
}

// DeleteMappingRule performs a soft-delete on a mapping rule (sets deleted_at).
// Only deletes within the transaction's organization scope. Returns an error if no matching row is found.
func DeleteMappingRule(tx WriteTxn, id uid.ID) error {
	query := querybuilder.New("UPDATE mapping_rules SET")
	query.B("deleted_at = now(), updated_at = now()")
	query.B("WHERE deleted_at is null AND organization_id = ? AND id = ?",
		tx.OrganizationID(), id)

	result, err := tx.Exec(query.String(), query.Args...)
	if err != nil {
		return handleError(err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("mapping rule not found")
	}
	return nil
}

// GetMappingRuleOptions holds the options for fetching a single mapping rule.
type GetMappingRuleOptions struct {
	ByID   uid.ID
	ByName string
}

// GetMappingRule fetches a single non-deleted mapping rule within the transaction's org scope.
func GetMappingRule(tx ReadTxn, opts GetMappingRuleOptions) (*models.MappingRule, error) {
	table := &mappingRulesTable{}
	query := querybuilder.New("SELECT")
	query.B(columnsForSelect(table))
	query.B("FROM mapping_rules")
	if opts.ByName != "" {
		query.B("WHERE deleted_at is null AND organization_id = ? AND rule_name = ?",
			tx.OrganizationID(), opts.ByName)
	} else {
		query.B("WHERE deleted_at is null AND organization_id = ? AND id = ?",
			tx.OrganizationID(), opts.ByID)
	}

	fields := table.ScanFields()
	err := tx.QueryRow(query.String(), query.Args...).Scan(fields...)
	if err != nil {
		return nil, handleError(err)
	}
	mapping := (*models.MappingRule)(table)
	return mapping, nil
}

// ListMappingRulesOptions holds the options for listing mapping rules.
type ListMappingRulesOptions struct {
	Name string // filters by rule_name (contains match)

	Pagination *Pagination
}

// ListMappingRules returns all non-deleted mapping rules in the transaction's org scope,
// optionally filtered by name and paginated. Results are ordered alphabetically by rule_name.
func ListMappingRules(tx ReadTxn, opts ListMappingRulesOptions) ([]models.MappingRule, error) {
	table := mappingRulesTable{}
	query := querybuilder.New("SELECT")
	query.B(columnsForSelect(table))
	if opts.Pagination != nil {
		query.B(", count(*) OVER()")
	}
	query.B("FROM mapping_rules")
	query.B("WHERE deleted_at is null AND organization_id = ?", tx.OrganizationID())

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

	var mappings []models.MappingRule
	for rows.Next() {
		table := mappingRulesTable{}
		fields := table.ScanFields()
		if opts.Pagination != nil {
			fields = append(fields, &opts.Pagination.TotalCount)
		}
		err := rows.Scan(fields...)
		if err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}
		mapping := (*models.MappingRule)(&table)
		mappings = append(mappings, *mapping)
	}

	return mappings, nil
}

// validateMappingRule enforces business rules on the model before DB persistence:
//   - rule_name and source_group_regex are required
//   - destination_type must be "kubernetes" or "ssh"
//   - name_template is required for all types
//   - role_template is required when destination_type is "kubernetes" (needed to determine RBAC role)
func validateMappingRule(mapping *models.MappingRule) error {
	if mapping.RuleName == "" {
		return fmt.Errorf("rule_name is required")
	}
	if mapping.SourceGroupRegex == "" {
		return fmt.Errorf("source_group_regex is required")
	}
	// Validate regex compiles -- prevents invalid patterns from being persisted
	// via direct data layer calls (migrations, seeds, tests).
	if _, err := regexp.Compile(mapping.SourceGroupRegex); err != nil {
		return fmt.Errorf("invalid source_group_regex: %w", err)
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
