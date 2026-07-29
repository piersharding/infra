// Tests for mapping rule validation at the data layer.
package data

import (
	"testing"

	"github.com/infrahq/infra/internal/server/models"
)

func TestValidateMappingRule(t *testing.T) {

	tests := []struct {
		name      string
		mapping   func() *models.MappingRule
		wantError bool
	}{
		{
			name: "valid kubernetes mapping with role_template",
			mapping: func() *models.MappingRule {
				return &models.MappingRule{
					RuleName:          "team-access",
					SourceGroupRegex:  "^team-(.*)$",
					DestinationType:   models.DestinationTypeKubernetes,
					NameTemplate:      "cluster-$1-prod",
					NamespaceTemplate: strPtr("ns-$1"),
					RoleTemplate:      strPtr("$1-admin"),
				}
			},
			wantError: false,
		},
		{
			name: "valid kubernetes mapping without namespace_template (optional)",
			mapping: func() *models.MappingRule {
				return &models.MappingRule{
					RuleName:         "team-access",
					SourceGroupRegex: "^team-(.*)$",
					DestinationType:  models.DestinationTypeKubernetes,
					NameTemplate:     "cluster-$1-prod",
					RoleTemplate:     strPtr("admin"),
				}
			},
			wantError: false,
		},
		{
			name: "valid SSH mapping without role_template (optional)",
			mapping: func() *models.MappingRule {
				return &models.MappingRule{
					RuleName:         "ssh-access",
					SourceGroupRegex: "^team-(.*)$",
					DestinationType:  models.DestinationTypeSSH,
					NameTemplate:     "host-$1",
				}
			},
			wantError: false,
		},
		{
			name: "valid SSH mapping with optional role_template set",
			mapping: func() *models.MappingRule {
				return &models.MappingRule{
					RuleName:         "ssh-access",
					SourceGroupRegex: "^team-(.*)$",
					DestinationType:  models.DestinationTypeSSH,
					NameTemplate:     "host-$1",
					RoleTemplate:     strPtr("deployer"),
				}
			},
			wantError: false,
		},
		{
			name: "empty rule_name returns error",
			mapping: func() *models.MappingRule {
				return &models.MappingRule{
					RuleName:         "",
					SourceGroupRegex: "^team-(.*)$",
					DestinationType:  models.DestinationTypeSSH,
					NameTemplate:     "host-$1",
				}
			},
			wantError: true,
		},
		{
			name: "empty source_group_regex returns error",
			mapping: func() *models.MappingRule {
				return &models.MappingRule{
					RuleName:         "test-rule",
					SourceGroupRegex: "",
					DestinationType:  models.DestinationTypeSSH,
					NameTemplate:     "host-$1",
				}
			},
			wantError: true,
		},
		{
			name: "invalid destination_type returns error",
			mapping: func() *models.MappingRule {
				return &models.MappingRule{
					RuleName:         "test-rule",
					SourceGroupRegex: "^team-(.*)$",
					DestinationType:  models.DestinationType("ldap"), // not kubernetes or ssh
					NameTemplate:     "host-$1",
				}
			},
			wantError: true,
		},
		{
			name: "empty name_template returns error",
			mapping: func() *models.MappingRule {
				return &models.MappingRule{
					RuleName:         "test-rule",
					SourceGroupRegex: "^team-(.*)$",
					DestinationType:  models.DestinationTypeSSH,
					NameTemplate:     "",
				}
			},
			wantError: true,
		},
		{
			name: "kubernetes without role_template returns error",
			mapping: func() *models.MappingRule {
				return &models.MappingRule{
					RuleName:         "team-access",
					SourceGroupRegex: "^team-(.*)$",
					DestinationType:  models.DestinationTypeKubernetes,
					NameTemplate:     "cluster-$1-prod",
					// RoleTemplate intentionally omitted
				}
			},
			wantError: true,
		},
		{
			name: "kubernetes with nil role_template returns error",
			mapping: func() *models.MappingRule {
				return &models.MappingRule{
					RuleName:         "team-access",
					SourceGroupRegex: "^team-(.*)$",
					DestinationType:  models.DestinationTypeKubernetes,
					NameTemplate:     "cluster-$1-prod",
					RoleTemplate:     nil,
				}
			},
			wantError: true,
		},
		{
			name: "kubernetes with empty string role_template returns error",
			mapping: func() *models.MappingRule {
				return &models.MappingRule{
					RuleName:         "team-access",
					SourceGroupRegex: "^team-(.*)$",
					DestinationType:  models.DestinationTypeKubernetes,
					NameTemplate:     "cluster-$1-prod",
					RoleTemplate:     strPtr(""),
				}
			},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mapping := tt.mapping()
			err := validateMappingRule(mapping)
			if (err != nil) != tt.wantError {
				t.Errorf("validateMappingRule() error = %v, wantErr %v", err, tt.wantError)
			}
		})
	}
}

func strPtr(s string) *string {
	return &s
}
