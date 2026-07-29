package api

import (
	"regexp"

	"github.com/infrahq/infra/internal/validate"
	"github.com/infrahq/infra/uid"
)

// ptrVal returns the string value of a pointer, or "" if nil.
func ptrVal(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// MappingRule represents a rule that matches groups to destinations.
// Each mapping defines:
//   - source_group_regex: regex pattern to match against identity provider group names
//   - destination_type: "kubernetes" or "ssh" — what kind of access to grant
//   - name_template: template (with $N capture references) for the resource name
//   - namespace_template (optional, k8s only): template for Kubernetes namespace scoping
//   - role_template (required for k8s): template for the RBAC role name
type MappingRule struct {
	ID                uid.ID             `json:"id"`
	Created           Time               `json:"created"`
	Updated           Time               `json:"updated"`
	RuleName          string             `json:"rule_name"`
	SourceGroupRegex  string             `json:"source_group_regex"`
	DestinationType   string             `json:"destination_type"`
	NameTemplate      string             `json:"name_template"`
	NamespaceTemplate string             `json:"namespace_template,omitempty"`
	RoleTemplate      string             `json:"role_template,omitempty"`
	MatchedGrants     []MappingRuleGrant `json:"matchedGrants"` // matched grants with privilege/resource details
}

// MappingRuleGrant represents a single auto-grant associated with a mapping rule.
type MappingRuleGrant struct {
	ID            uid.ID `json:"id"`
	GroupID       uid.ID `json:"groupID"`
	GroupName     string `json:"groupName"`
	Privilege     string `json:"privilege"`
	Resource      string `json:"resource"`
	DestinationID uid.ID `json:"destinationID,omitempty"`
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
	return validateMappingRuleRequest(MappingRule{
		RuleName:          r.RuleName,
		SourceGroupRegex:  r.SourceGroupRegex,
		DestinationType:   r.DestinationType,
		NameTemplate:      r.NameTemplate,
		NamespaceTemplate: ptrVal(r.NamespaceTemplate),
		RoleTemplate:      ptrVal(r.RoleTemplate),
	})
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
	return validateMappingRuleRequest(MappingRule{
		RuleName:          r.RuleName,
		SourceGroupRegex:  r.SourceGroupRegex,
		DestinationType:   r.DestinationType,
		NameTemplate:      r.NameTemplate,
		NamespaceTemplate: ptrVal(r.NamespaceTemplate),
		RoleTemplate:      ptrVal(r.RoleTemplate),
	})
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

// MappingRuleEvalStatus is the response body for the eval-status endpoint.
// It exposes the result of the last asynchronous evaluation so the UI can
// indicate whether auto-grants were computed successfully.
type MappingRuleEvalStatus struct {
	LastRunAt Time   `json:"last_run_at"`
	Success   bool   `json:"success"`
	Error     string `json:"error,omitempty"`
}

// ListMappingRulesResponse is the response body for listing mapping rules.
type ListMappingRulesResponse struct {
	Count  int           `json:"count"`
	Result []MappingRule `json:"result"`
}

// validateMappingRuleRequest validates common MappingRule fields and returns validation rules.
// Both Create and Update request types delegate to this shared function to avoid duplication.
func validateMappingRuleRequest(rule MappingRule) []validate.ValidationRule {
	rules := []validate.ValidationRule{
		validate.Required("rule_name", rule.RuleName),
		validate.Required("source_group_regex", rule.SourceGroupRegex),
		validate.Required("destination_type", rule.DestinationType),
		validate.Enum("destination_type", rule.DestinationType, []string{"kubernetes", "ssh"}),
		validate.Required("name_template", rule.NameTemplate),
	}

	if !isValidRegexp(rule.SourceGroupRegex) {
		rules = append(rules, validate.ValidatorFunc(func() *validate.Failure {
			return &validate.Failure{
				Name:     "source_group_regex",
				Problems: []string{"must be a valid regular expression"},
			}
		}))
	}

	if rule.DestinationType == "kubernetes" && (rule.RoleTemplate == "") {
		rules = append(rules, validate.ValidatorFunc(func() *validate.Failure {
			return &validate.Failure{
				Name:     "role_template",
				Problems: []string{"is required for kubernetes destinations"},
			}
		}))
	}

	// Note: source_group_regex is also validated in data.validateMappingRule() for
	// defense-in-depth — direct callers (migrations, seeds, tests) that bypass the API
	// still get regex validation at the persistence layer. This duplication is intentional.
	return rules
}

// isValidRegexp checks whether a string compiles as a valid Go regular expression.
// Used by both Create and Update validation to reject invalid patterns at the API layer.
func isValidRegexp(s string) bool {
	_, err := regexp.Compile(s)
	return err == nil
}
