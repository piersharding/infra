package models

import (
	"github.com/infrahq/infra/api"
	"github.com/infrahq/infra/uid"
)

// DestinationType enumerates the kinds of destinations that group mappings can target.
type DestinationType string

const (
	DestinationTypeKubernetes DestinationType = "kubernetes"
	DestinationTypeSSH        DestinationType = "ssh"
)

// GroupMapping defines a rule that matches identity provider groups (via regex) to
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
type GroupMapping struct {
	Model
	OrganizationMember

	CreatedBy         uid.ID          `db:"created_by"`
	RuleName          string          `json:"rule_name"`
	SourceGroupRegex  string          `json:"source_group_regex"`
	DestinationType   DestinationType `json:"destination_type"`
	NameTemplate      string          `json:"name_template"`
	NamespaceTemplate *string         `json:"namespace_template,omitempty"`
	RoleTemplate      *string         `json:"role_template,omitempty"`

	UpdateIndex int64 `db:"-"`
}

func (g *GroupMapping) ToAPI() *api.GroupMapping {
	m := &api.GroupMapping{
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
