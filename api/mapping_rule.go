package api

import (
	"regexp"

	"github.com/infrahq/infra/internal/validate"
	"github.com/infrahq/infra/uid"
)

// MappingRule represents a rule that matches groups to destinations.
// Each mapping defines:
//   - source_group_regex: regex pattern to match against identity provider group names
//   - destination_type: "kubernetes" or "ssh" — what kind of access to grant
//   - name_template: template (with $N capture references) for the resource name
//   - namespace_template (optional, k8s only): template for Kubernetes namespace scoping
//   - role_template (required for k8s): template for the RBAC role name
type MappingRule struct {
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

// CreateMappingRuleRequest is the request body for creating a mapping rule.
// RoleTemplate and NamespaceTemplate are pointers to allow distinguishing "not set" from "empty string".
type CreateMappingRuleRequest struct {
	RuleName          string  `json:"rule_name"`
	SourceGroupRegex  string  `json:"source_group_regex"`
	DestinationType   string  `json:"destination_type"`
	NameTemplate      string  `json:"name_template"`
	NamespaceTemplate *string `json:"namespace_template,omitempty"`
	RoleTemplate      *string `json:"role_template,omitempty"`
}

// ValidationRules enforces:
//
//   - rule_name, source_group_regex, destination_type, and name_template are all non-empty
//   - source_group_regex compiles as a valid Go regexp
//   - for kubernetes destinations: role_template is required (needed to determine RBAC role)
func (r CreateMappingRuleRequest) ValidationRules() []validate.ValidationRule {
	rules := []validate.ValidationRule{
		validate.Required("rule_name", r.RuleName),
		validate.Required("source_group_regex", r.SourceGroupRegex),
		validate.Required("destination_type", r.DestinationType),
		validate.Enum("destination_type", r.DestinationType, []string{"kubernetes", "ssh"}),
		validate.Required("name_template", r.NameTemplate),
	}

	if !isValidRegexp(r.SourceGroupRegex) {
		rules = append(rules, validate.ValidatorFunc(func() *validate.Failure {
			return &validate.Failure{
				Name:     "source_group_regex",
				Problems: []string{"must be a valid regular expression"},
			}
		}))
	}

	if r.DestinationType == "kubernetes" && (r.RoleTemplate == nil || *r.RoleTemplate == "") {
		rules = append(rules, validate.ValidatorFunc(func() *validate.Failure {
			return &validate.Failure{
				Name:     "role_template",
				Problems: []string{"is required for kubernetes destinations"},
			}
		}))
	}

	return rules
}

// UpdateMappingRuleRequest is the request body for updating an existing mapping rule.
// ID comes from the URL path; other fields mirror CreateMappingRuleRequest.
type UpdateMappingRuleRequest struct {
	ID                uid.ID  `uri:"id" json:"-"`
	RuleName          string  `json:"rule_name"`
	SourceGroupRegex  string  `json:"source_group_regex"`
	DestinationType   string  `json:"destination_type"`
	NameTemplate      string  `json:"name_template"`
	NamespaceTemplate *string `json:"namespace_template,omitempty"`
	RoleTemplate      *string `json:"role_template,omitempty"`
}

func (r UpdateMappingRuleRequest) ValidationRules() []validate.ValidationRule {
	rules := []validate.ValidationRule{
		validate.Required("rule_name", r.RuleName),
		validate.Required("source_group_regex", r.SourceGroupRegex),
		validate.Required("destination_type", r.DestinationType),
		validate.Enum("destination_type", r.DestinationType, []string{"kubernetes", "ssh"}),
		validate.Required("name_template", r.NameTemplate),
	}

	if !isValidRegexp(r.SourceGroupRegex) {
		rules = append(rules, validate.ValidatorFunc(func() *validate.Failure {
			return &validate.Failure{
				Name:     "source_group_regex",
				Problems: []string{"must be a valid regular expression"},
			}
		}))
	}

	if r.DestinationType == "kubernetes" && (r.RoleTemplate == nil || *r.RoleTemplate == "") {
		rules = append(rules, validate.ValidatorFunc(func() *validate.Failure {
			return &validate.Failure{
				Name:     "role_template",
				Problems: []string{"is required for kubernetes destinations"},
			}
		}))
	}

	return rules
}

// ListMappingRulesRequest provides optional name filter and pagination parameters
// for listing mapping rules.
// The "name" query param triggers a case-insensitive substring match on rule_name.
type ListMappingRulesRequest struct {
	Name string `form:"name"`
	PaginationRequest
}

func (r ListMappingRulesRequest) ValidationRules() []validate.ValidationRule {
	return nil
}

// ListMappingRulesResponse is the response body for listing mapping rules.
type ListMappingRulesResponse struct {
	Count  int           `json:"count"`
	Result []MappingRule `json:"result"`
}

// isValidRegexp checks whether a string compiles as a valid Go regular expression.
// Used by both Create and Update validation to reject invalid patterns at the API layer.
func isValidRegexp(s string) bool {
	_, err := regexp.Compile(s)
	return err == nil
}
