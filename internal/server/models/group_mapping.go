package models

import (
	"github.com/infrahq/infra/api"
	"github.com/infrahq/infra/uid"
)

type DestinationType string

const (
	DestinationTypeKubernetes DestinationType = "kubernetes"
	DestinationTypeSSH        DestinationType = "ssh"
)

// GroupMapping defines a rule that matches groups to destinations,
// automatically granting matched groups access using template strings.
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
		ID:                g.ID,
		Created:           api.Time(g.CreatedAt),
		Updated:           api.Time(g.UpdatedAt),
		RuleName:          g.RuleName,
		SourceGroupRegex:  g.SourceGroupRegex,
		DestinationType:   string(g.DestinationType),
		NameTemplate:      g.NameTemplate,
	}

	if g.NamespaceTemplate != nil {
		m.NamespaceTemplate = *g.NamespaceTemplate
	}

	if g.RoleTemplate != nil {
		m.RoleTemplate = *g.RoleTemplate
	}

	return m
}
