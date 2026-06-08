package data

import (
	"fmt"
	"time"

	"github.com/infrahq/infra/internal/server/data/querybuilder"
	"github.com/infrahq/infra/internal/server/models"
	"github.com/infrahq/infra/uid"
)

type groupsTable models.Group

func (g groupsTable) Table() string {
	return "groups"
}

func (g groupsTable) Columns() []string {
	return []string{"created_at", "created_by", "created_by_provider", "deleted_at", "id", "name", "organization_id", "updated_at"}
}

func (g groupsTable) Values() []any {
	return []any{g.CreatedAt, g.CreatedBy, g.CreatedByProvider, g.DeletedAt, g.ID, g.Name, g.OrganizationID, g.UpdatedAt}
}

func (g *groupsTable) ScanFields() []any {
	return []any{&g.CreatedAt, &g.CreatedBy, &g.CreatedByProvider, &g.DeletedAt, &g.ID, &g.Name, &g.OrganizationID, &g.UpdatedAt}
}

func CreateGroup(tx WriteTxn, group *models.Group) error {
	return insert(tx, (*groupsTable)(group))
}

type GetGroupOptions struct {
	// ByID instructs GetGroup to return the group matching this ID.
	ByID uid.ID
	// ByName instructs GetGroup to return the group matching this name.
	ByName string
	// IncludeDeleted, if true, returns groups regardless of deleted_at status.
	// Used internally by cleanupOrphanedGroups and mapping rule evaluation where
	// we need to see all groups including soft-deleted ones for consistency checks.
	IncludeDeleted bool
}

func GetGroup(tx ReadTxn, opts GetGroupOptions) (*models.Group, error) {
	group := &groupsTable{}
	query := querybuilder.New("SELECT")
	query.B(columnsForSelect(group))
	query.B("FROM groups")
	if !opts.IncludeDeleted {
		query.B("WHERE deleted_at is null")
	} else {
		query.B("WHERE 1=1")
	}
	query.B("AND organization_id = ?", tx.OrganizationID())

	switch {
	case opts.ByID != 0:
		query.B("AND id = ?", opts.ByID)
	case opts.ByName != "":
		query.B("AND name = ?", opts.ByName)
	default:
		return nil, fmt.Errorf("GetGroup requires an ID")
	}

	err := tx.QueryRow(query.String(), query.Args...).Scan(group.ScanFields()...)
	if err != nil {
		return nil, handleError(err)
	}

	count, err := countUsersInGroup(tx, group.ID)
	if err != nil {
		return nil, err
	}
	group.TotalUsers = int(count)
	return (*models.Group)(group), nil
}

// LocalOnly filters ListGroups to only locally created groups
// (created_by_provider IS NULL OR = 0). Used by filterIDPGroups and other
// internal paths that need to distinguish admin-created from IDP-synced groups.
type ListGroupsOptions struct {
	ByName string
	ByIDs  []uid.ID

	// ByGroupMember instructs ListGroups to return groups where this user ID
	// is a member of the group.
	ByGroupMember uid.ID

	// LocalOnly, if true, returns only locally created groups (created_by_provider IS NULL OR = 0).
	LocalOnly bool

	Pagination *Pagination
}

func ListGroups(tx ReadTxn, opts ListGroupsOptions) ([]models.Group, error) {
	table := groupsTable{}
	query := querybuilder.New("SELECT")
	query.B(columnsForSelect(table))
	if opts.Pagination != nil {
		query.B(", count(*) OVER()")
	}
	query.B("FROM groups")
	if opts.ByGroupMember != 0 {
		query.B("JOIN identities_groups ON groups.id = identities_groups.group_id")
		query.B("AND identities_groups.identity_id = ?", opts.ByGroupMember)
	}
	query.B("WHERE deleted_at is null")
	query.B("AND organization_id = ?", tx.OrganizationID())

	if opts.LocalOnly {
		query.B("AND (created_by_provider IS NULL OR created_by_provider = 0)")
	}

	if opts.ByName != "" {
		query.B("AND name ILIKE ?", "%"+opts.ByName+"%")
	}
	if len(opts.ByIDs) > 0 {
		query.B("AND groups.id IN")
		queryInClause(query, opts.ByIDs)
	}

	query.B("ORDER BY name ASC")
	if opts.Pagination != nil {
		opts.Pagination.PaginateQuery(query)
	}

	rows, err := tx.Query(query.String(), query.Args...)
	if err != nil {
		return nil, err
	}
	result, err := scanRows(rows, func(group *models.Group) []any {
		fields := (*groupsTable)(group).ScanFields()
		if opts.Pagination != nil {
			fields = append(fields, &opts.Pagination.TotalCount)
		}
		return fields
	})
	if err != nil {
		return nil, err
	}

	// TODO: do this in a single query
	for i := range result {
		count, err := countUsersInGroup(tx, result[i].ID)
		if err != nil {
			return nil, err
		}
		result[i].TotalUsers = int(count)
	}
	return result, nil
}

func ListGroupIDsForUser(tx ReadTxn, userID uid.ID) ([]uid.ID, error) {
	stmt := `SELECT DISTINCT group_id FROM identities_groups WHERE identity_id = ?`
	rows, err := tx.Query(stmt, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []uid.ID
	for rows.Next() {
		var id uid.ID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		result = append(result, id)
	}
	return result, rows.Err()
}

func DeleteGroup(tx WriteTxn, id uid.ID) error {
	err := DeleteGrants(tx, DeleteGrantsOptions{BySubject: models.NewSubjectForGroup(id)})
	if err != nil {
		return fmt.Errorf("remove grants: %w", err)
	}

	_, err = tx.Exec(`DELETE from identities_groups WHERE group_id = ?`, id)
	if err != nil {
		return fmt.Errorf("remove users from group: %w", err)
	}

	stmt := `
		UPDATE groups
		SET deleted_at = ?
		WHERE id = ?
		AND deleted_at is null
		AND organization_id = ?`
	_, err = tx.Exec(stmt, time.Now(), id, tx.OrganizationID())
	return handleError(err)
}

func AddUsersToGroup(tx WriteTxn, groupID uid.ID, idsToAdd []uid.ID) error {
	query := querybuilder.New("INSERT INTO identities_groups(group_id, identity_id)")
	query.B("VALUES")
	for i, id := range idsToAdd {
		query.B("(?, ?)", groupID, id)
		if i+1 != len(idsToAdd) {
			query.B(",")
		}
	}
	query.B("ON CONFLICT DO NOTHING")

	_, err := tx.Exec(query.String(), query.Args...)
	return handleError(err)
}

// RemoveUsersFromGroup removes any user ID listed in idsToRemove from the group
// with ID groupID.
// Note that DeleteGroup also removes users from the group.
func RemoveUsersFromGroup(tx WriteTxn, groupID uid.ID, idsToRemove []uid.ID) error {
	query := querybuilder.New(`DELETE FROM identities_groups`)
	query.B(`WHERE group_id = ?`, groupID)
	query.B(`AND identity_id IN`)
	queryInClause(query, idsToRemove)
	_, err := tx.Exec(query.String(), query.Args...)
	return handleError(err)
}

func countUsersInGroup(tx ReadTxn, groupID uid.ID) (int64, error) {
	stmt := `SELECT count(*) FROM identities_groups WHERE group_id = ?`
	var count int64
	err := tx.QueryRow(stmt, groupID).Scan(&count)
	return count, handleError(err)
}

// CountAllGroups counts all non-deleted groups for an organization.
func CountAllGroups(tx ReadTxn) (int64, error) {
	return countRows(tx, groupsTable{})
}

// ListGroupNames returns a set of non-deleted group names for the current org,
// optionally filtered to only locally created groups.
func ListGroupNames(tx ReadTxn, opts ListGroupsOptions) (map[string]bool, error) {
	query := querybuilder.New("SELECT name FROM groups")
	if opts.ByGroupMember != 0 {
		query.B("JOIN identities_groups ON groups.id = identities_groups.group_id")
	}
	query.B("WHERE deleted_at is null AND organization_id = ?", tx.OrganizationID())

	if opts.LocalOnly {
		query.B("AND (created_by_provider IS NULL OR created_by_provider = 0)")
	}

	rows, err := tx.Query(query.String(), query.Args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	names := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan group name: %w", err)
		}
		names[name] = true
	}
	return names, rows.Err()
}

// GetUsersInGroup returns all distinct user (identity) IDs that are members of the
// group identified by groupID. Queries the identities_groups join table directly.
// Returns an empty slice (not nil) when the group has no members.
func GetUsersInGroup(tx ReadTxn, groupID uid.ID) ([]uid.ID, error) {
	query := querybuilder.New(`SELECT DISTINCT identity_id FROM identities_groups WHERE group_id = ?`)
	rows, err := tx.Query(query.String(), groupID)
	if err != nil {
		return nil, handleError(err)
	}
	defer rows.Close()

	var userIDs []uid.ID
	for rows.Next() {
		var id uid.ID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan identity_id: %w", err)
		}
		userIDs = append(userIDs, id)
	}

	if err := rows.Err(); err != nil {
		return nil, handleError(err)
	}

	return userIDs, nil
}
