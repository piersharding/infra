package api

import (
	"regexp"

	"github.com/infrahq/infra/internal/validate"
	"github.com/infrahq/infra/uid"
)

// GroupMapping represents a rule that matches groups to destinations.
type GroupMapping struct {
	ID                uid.ID `json:"id"`
	Created           Time   `json:"created"`
	Updated           Time   `json:"updated"`
	RuleName          string `json:"rule_name"`
	SourceGroupRegex  string `json:"source_group_regex"`
	DestinationType   string `json:"destination_type"`
	NameTemplate      string `json:"name_template"`
	NamespaceTemplate string `json:"namespace_template,omitempty"`
	RoleTemplate      string `json:"role_template,omitempty"`
}

// CreateGroupMappingRequest is the request body for creating a group mapping.
type CreateGroupMappingRequest struct {
	RuleName          string `json:"rule_name"`
	SourceGroupRegex  string `json:"source_group_regex"`
	DestinationType   string `json:"destination_type"`
	NameTemplate      string `json:"name_template"`
	NamespaceTemplate *string `json:"namespace_template,omitempty"`
	RoleTemplate      *string `json:"role_template,omitempty"`
}

func (r CreateGroupMappingRequest) ValidationRules() []validate.ValidationRule {
	rules := []validate.ValidationRule{
		validate.Required("rule_name", r.RuleName),
		validate.Required("source_group_regex", r.SourceGroupRegex),
		validate.Required("destination_type", r.DestinationType),
		validate.Enum("destination_type", r.DestinationType, []string{"kubernetes", "ssh"}),
		validate.Required("name_template", r.NameTemplate),
	}

	if !isValidRegexp(r.SourceGroupRegex) {
		rules = append(rules, validate.Fail("source_group_regex", "must be a valid regular expression"))
	}

	if r.DestinationType == "kubernetes" && (r.RoleTemplate == nil || *r.RoleTemplate == "") {
		rules = append(rules, validate.Fail("role_template", "is required for kubernetes destinations"))
	}

	return rules
}

// UpdateGroupMappingRequest is the request body for updating a group mapping.
type UpdateGroupMappingRequest struct {
	ID                uid.ID `uri:"id" json:"-"`
	RuleName          string `json:"rule_name"`
	SourceGroupRegex  string `json:"source_group_regex"`
	DestinationType   string `json:"destination_type"`
	NameTemplate      string `json:"name_template"`
	NamespaceTemplate *string `json:"namespace_template,omitempty"`
	RoleTemplate      *string `json:"role_template,omitempty"`
}

func (r UpdateGroupMappingRequest) ValidationRules() []validate.ValidationRule {
	rules := []validate.ValidationRule{
		validate.Required("rule_name", r.RuleName),
		validate.Required("source_group_regex", r.SourceGroupRegex),
		validate.Required("destination_type", r.DestinationType),
		validate.Enum("destination_type", r.DestinationType, []string{"kubernetes", "ssh"}),
		validate.Required("name_template", r.NameTemplate),
	}

	if !isValidRegexp(r.SourceGroupRegex) {
		rules = append(rules, validate.Fail("source_group_regex", "must be a valid regular expression"))
	}

	if r.DestinationType == "kubernetes" && (r.RoleTemplate == nil || *r.RoleTemplate == "") {
		rules = append(rules, validate.Fail("role_template", "is required for kubernetes destinations"))
	}

	return rules
}

// ListGroupMappingsRequest is the request query parameters for listing group mappings.
type ListGroupMappingsRequest struct {
	Name string `form:"name"`
	PaginationRequest
}

func (r ListGroupMappingsRequest) ValidationRules() []validate.ValidationRule {
	return nil
}

// ListGroupMappingsResponse is the response body for listing group mappings.
type ListGroupMappingsResponse struct {
	Count  int               `json:"count"`
	Result []GroupMapping    `json:"result"`
}

func isValidRegexp(s string) bool {
	_, err := regexp.Compile(s)
	return err == nil
}
