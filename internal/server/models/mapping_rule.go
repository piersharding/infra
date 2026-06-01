package models

import (
	"fmt"

	"github.com/infrahq/infra/api"
	"github.com/infrahq/infra/uid"
)

// DestinationType enumerates the kinds of destinations that group mappings can target.
type DestinationType string

const (
	DestinationTypeKubernetes DestinationType = "kubernetes"
	DestinationTypeSSH        DestinationType = "ssh"
)

// MappingRuleReplacement defines a single role name replacement.
// From is the source role name; To is the list of roles it should be replaced with.
type MappingRuleReplacement struct {
	From string   `config:"from"`
	To   []string `config:"to"`
}

// Validate checks that this replacement has non-empty from and to values,
// returning an error describing what is wrong.
func (r *MappingRuleReplacement) Validate() error {
	if r.From == "" {
		return fmt.Errorf("role replacement 'from' must not be empty")
	}
	if len(r.To) == 0 {
		return fmt.Errorf("role replacement for %q: 'to' must have at least one value", r.From)
	}
	for _, t := range r.To {
		if t == "" {
			return fmt.Errorf("role replacement for %q: 'to' entries must not be empty", r.From)
		}
	}
	return nil
}

// MappingRule defines a rule that matches identity provider groups (via regex) to
// destinations, automatically creating grants with templated resource/role names.
//
// Workflow:
// 1. A mapping's source_group_regex is matched against each group name from the IDP
// 2. If it matches, templates are applied using $N capture-group references to produce:
//   - Resource name (name_template) — e.g., "cluster-platform-prod"
//   - Role name (role_template) for k8s — e.g., "platform-admin"
//   - Namespace (namespace_template, optional for k8s) — e.g., "platform-ns"
//
// 3. Grants are created with CreatedBy="system" so they can be cleaned up if rules change
type MappingRule struct {
	Model
	OrganizationMember

	CreatedBy         uid.ID          `db:"created_by"`
	RuleName          string          `json:"rule_name"`
	SourceGroupRegex  string          `json:"source_group_regex"`
	DestinationType   DestinationType `json:"destination_type"`
	NameTemplate      string          `json:"name_template"`
	NamespaceTemplate *string         `json:"namespace_template,omitempty"`
	RoleTemplate      *string         `json:"role_template,omitempty"`
}

func (g *MappingRule) ToAPI() *api.MappingRule {
	m := &api.MappingRule{
		ID:               g.ID,
		Created:          api.Time(g.CreatedAt),
		Updated:          api.Time(g.UpdatedAt),
		RuleName:         g.RuleName,
		SourceGroupRegex: g.SourceGroupRegex,
		DestinationType:  string(g.DestinationType),
		NameTemplate:     g.NameTemplate,
	}

	if g.NamespaceTemplate != nil {
		m.NamespaceTemplate = *g.NamespaceTemplate
	}

	if g.RoleTemplate != nil {
		m.RoleTemplate = *g.RoleTemplate
	}

	return m
}
