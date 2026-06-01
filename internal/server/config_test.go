package server

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"testing"

	"golang.org/x/crypto/bcrypt"
	"gotest.tools/v3/assert"
	is "gotest.tools/v3/assert/cmp"

	"github.com/infrahq/infra/internal"
	"github.com/infrahq/infra/internal/server/data"
	"github.com/infrahq/infra/internal/server/models"
)

func TestLoadConfigEmpty(t *testing.T) {
	s := setupServer(t)

	err := s.loadConfig(BootstrapConfig{})
	assert.NilError(t, err)
}

func TestLoadConfigWithUsers(t *testing.T) {
	s := setupServer(t)

	config := BootstrapConfig{
		Users: []User{
			{
				Name: "bob@example.com",
			},
			{
				Name:     "alice@example.com",
				Password: "password",
			},
			{
				Name:      "sue@example.com",
				AccessKey: "aaaaaaaaaa.bbbbbbbbbbbbbbbbbbbbbbbb",
			},
			{
				Name:      "jim@example.com",
				Password:  "password",
				AccessKey: "bbbbbbbbbb.bbbbbbbbbbbbbbbbbbbbbbbb",
			},
		},
	}

	err := s.loadConfig(config)
	assert.NilError(t, err)

	user, _, _ := getTestDefaultOrgUserDetails(t, s, "bob@example.com")
	assert.Equal(t, "bob@example.com", user.Name)

	user, creds, _ := getTestDefaultOrgUserDetails(t, s, "alice@example.com")
	assert.Equal(t, "alice@example.com", user.Name)
	err = bcrypt.CompareHashAndPassword(creds.PasswordHash, []byte("password"))
	assert.NilError(t, err)

	user, _, key := getTestDefaultOrgUserDetails(t, s, "sue@example.com")
	assert.Equal(t, "sue@example.com", user.Name)
	assert.Equal(t, key.KeyID, "aaaaaaaaaa")
	chksm := sha256.Sum256([]byte("bbbbbbbbbbbbbbbbbbbbbbbb"))
	assert.Equal(t, bytes.Compare(key.SecretChecksum, chksm[:]), 0) // 0 means the byte slices are equal

	user, creds, key = getTestDefaultOrgUserDetails(t, s, "jim@example.com")
	assert.Equal(t, "jim@example.com", user.Name)
	err = bcrypt.CompareHashAndPassword(creds.PasswordHash, []byte("password"))
	assert.NilError(t, err)
	assert.Equal(t, key.KeyID, "bbbbbbbbbb")
	chksm = sha256.Sum256([]byte("bbbbbbbbbbbbbbbbbbbbbbbb"))
	assert.Equal(t, bytes.Compare(key.SecretChecksum, chksm[:]), 0) // 0 means the byte slices are equal
}

func TestLoadConfigUpdate(t *testing.T) {
	s := setupServer(t)

	config := BootstrapConfig{
		Users: []User{
			{
				Name:      "r2d2@example.com",
				InfraRole: "admin",
			},
			{
				Name:      "c3po@example.com",
				AccessKey: "TllVlekkUz.NFnxSlaPQLosgkNsyzaMttfC",
				InfraRole: "view",
			},
			{
				Name:     "sarah@email.com",
				Password: "supersecret",
			},
		},
	}

	err := s.loadConfig(config)
	assert.NilError(t, err)

	tx := txnForTestCase(t, s.db, s.db.DefaultOrg.ID)
	defaultOrg := s.db.DefaultOrg

	var identities, credentials, accessKeys int64

	grants, err := data.ListGrants(tx, data.ListGrantsOptions{})
	assert.NilError(t, err)
	assert.Assert(t, is.Len(grants, 3)) // 2 from config, 1 internal connector

	privileges := map[string]int{
		"admin":     0,
		"view":      0,
		"connector": 0,
	}

	for _, v := range grants {
		privileges[v.Privilege]++
	}

	assert.Equal(t, privileges["admin"], 1)
	assert.Equal(t, privileges["view"], 1)
	assert.Equal(t, privileges["connector"], 1)

	err = tx.QueryRow("SELECT COUNT(*) FROM identities WHERE organization_id = ? AND deleted_at IS null;", defaultOrg.ID).Scan(&identities)
	assert.NilError(t, err)
	assert.Equal(t, int64(4), identities)

	err = tx.QueryRow("SELECT COUNT(*) FROM credentials WHERE organization_id = ?;", defaultOrg.ID).Scan(&credentials)
	assert.NilError(t, err)
	assert.Equal(t, int64(1), credentials) // sarah@example.com

	err = tx.QueryRow("SELECT COUNT(*) FROM access_keys WHERE organization_id = ?;", defaultOrg.ID).Scan(&accessKeys)
	assert.NilError(t, err)
	assert.Equal(t, int64(1), accessKeys) // c3po

	updatedConfig := BootstrapConfig{
		Users: []User{
			{
				Name:      "c3po@example.com",
				InfraRole: "admin",
			},
		},
	}

	err = s.loadConfig(updatedConfig)
	assert.NilError(t, err)

	grants, err = data.ListGrants(tx, data.ListGrantsOptions{})
	assert.NilError(t, err)
	assert.Assert(t, is.Len(grants, 4))

	privileges = map[string]int{
		"admin":     0,
		"view":      0,
		"connector": 0,
	}

	for _, v := range grants {
		privileges[v.Privilege]++
	}

	assert.Equal(t, privileges["admin"], 2)
	assert.Equal(t, privileges["view"], 1)
	assert.Equal(t, privileges["connector"], 1)

	err = tx.QueryRow("SELECT COUNT(*) FROM identities WHERE organization_id = ? AND deleted_at IS null;", defaultOrg.ID).Scan(&identities)
	assert.NilError(t, err)
	assert.Equal(t, int64(4), identities)
}

func TestLoadAccessKey(t *testing.T) {
	s := setupServer(t)

	// access key that we will attempt to assign to multiple users
	testAccessKey := Secret("aaaaaaaaaa.bbbbbbbbbbbbbbbbbbbbbbbb")

	// create a user and assign them an access key
	bob := &models.Identity{Name: "bob@example.com"}
	err := data.CreateIdentity(s.DB(), bob)
	assert.NilError(t, err)

	err = s.loadAccessKey(s.DB(), bob, testAccessKey)
	assert.NilError(t, err)

	t.Run("access key can be reloaded for the same identity it was issued for", func(t *testing.T) {
		err = s.loadAccessKey(s.DB(), bob, testAccessKey)
		assert.NilError(t, err)
	})

	t.Run("duplicate access key ID is rejected", func(t *testing.T) {
		alice := &models.Identity{Name: "alice@example.com"}
		err = data.CreateIdentity(s.DB(), alice)
		assert.NilError(t, err)

		err = s.loadAccessKey(s.DB(), alice, testAccessKey)
		assert.Error(t, err, "access key assigned to \"alice@example.com\" is already assigned to another user, a user's access key must have a unique ID")
	})
}

// getTestDefaultOrgUserDetails gets the attributes of a user created from a config file
// --- Mapping Rule Tests ---

func TestLoadMappingRulesCreate(t *testing.T) {
	s := setupServer(t)

	config := BootstrapConfig{
		MappingRules: []MappingRuleConfig{
			{
				Name:              "platform-admins",
				SourceGroupRegex:  `platform-.*`,
				DestinationType:   "kubernetes",
				NameTemplate:      "cluster-platform-prod",
				NamespaceTemplate: "platform",
				RoleTemplate:      strPtr("platform-admin"),
			},
			{
				Name:              "ssh-access",
				SourceGroupRegex:  `ssh-.*`,
				DestinationType:   "ssh",
				NameTemplate:      "bastion-hosts",
				NamespaceTemplate: "",
			},
		},
	}

	err := s.loadConfig(config)
	assert.NilError(t, err)

	tx := txnForTestCase(t, s.db, s.db.DefaultOrg.ID)

	rule, err := data.GetMappingRule(tx, data.GetMappingRuleOptions{ByName: "platform-admins"})
	assert.NilError(t, err)
	assert.Equal(t, rule.RuleName, "platform-admins")
	assert.Assert(t, is.DeepEqual(rule.SourceGroupRegex, `platform-.*`))
	assert.Equal(t, string(rule.DestinationType), "kubernetes")
	assert.Equal(t, rule.NameTemplate, "cluster-platform-prod")
	assert.Assert(t, rule.NamespaceTemplate != nil)
	assert.Equal(t, *rule.NamespaceTemplate, "platform")
	assert.Assert(t, rule.RoleTemplate != nil)
	assert.Equal(t, *rule.RoleTemplate, "platform-admin")

	rule2, err := data.GetMappingRule(tx, data.GetMappingRuleOptions{ByName: "ssh-access"})
	assert.NilError(t, err)
	assert.Equal(t, rule2.RuleName, "ssh-access")
	assert.Assert(t, is.DeepEqual(rule2.SourceGroupRegex, `ssh-.*`))
	assert.Equal(t, string(rule2.DestinationType), "ssh")
	assert.Equal(t, rule2.NameTemplate, "bastion-hosts")
	assert.Assert(t, rule2.NamespaceTemplate == nil)
	assert.Assert(t, rule2.RoleTemplate == nil)
}

func TestLoadMappingRulesUpsert(t *testing.T) {
	s := setupServer(t)

	// First load: create a rule
	config1 := BootstrapConfig{
		MappingRules: []MappingRuleConfig{
			{
				Name:              "platform-admins",
				SourceGroupRegex:  `platform-.*`,
				DestinationType:   "kubernetes",
				NameTemplate:      "cluster-platform-prod",
				NamespaceTemplate: "platform",
				RoleTemplate:      strPtr("platform-admin"),
			},
		},
	}

	err := s.loadConfig(config1)
	assert.NilError(t, err)

	tx := txnForTestCase(t, s.db, s.db.DefaultOrg.ID)
	originalRule, err := data.GetMappingRule(tx, data.GetMappingRuleOptions{ByName: "platform-admins"})
	assert.NilError(t, err)

	// Second load: update the same rule (same name, different values)
	config2 := BootstrapConfig{
		MappingRules: []MappingRuleConfig{
			{
				Name:              "platform-admins",
				SourceGroupRegex:  `platform-v2-.*`,
				DestinationType:   "kubernetes",
				NameTemplate:      "cluster-platform-prod-v2",
				NamespaceTemplate: "platform-v2",
				RoleTemplate:      strPtr("platform-admin"),
			},
		},
	}

	err = s.loadConfig(config2)
	assert.NilError(t, err)

	// Rule should still be found by same name with updated values
	updatedRule, err := data.GetMappingRule(tx, data.GetMappingRuleOptions{ByName: "platform-admins"})
	assert.NilError(t, err)
	assert.Equal(t, originalRule.ID, updatedRule.ID) // Same record (not re-created)
	assert.Assert(t, is.DeepEqual(updatedRule.SourceGroupRegex, `platform-v2-.*`))

	// Original name should no longer exist as a separate rule.
	_, err = data.GetMappingRule(tx, data.GetMappingRuleOptions{ByName: "old-platform-admins"})
	assert.ErrorIs(t, err, internal.ErrNotFound)
}

func TestLoadMappingRulesValidationErrors(t *testing.T) {
	s := setupServer(t)

	t.Run("missing name returns error", func(t *testing.T) {
		config := BootstrapConfig{
			MappingRules: []MappingRuleConfig{
				{SourceGroupRegex: `platform-.*`, DestinationType: "kubernetes", NameTemplate: "cluster"},
			},
		}

		err := s.loadConfig(config)
		assert.ErrorContains(t, err, "mapping rule name")
	})

	t.Run("invalid regex returns error", func(t *testing.T) {
		config := BootstrapConfig{
			MappingRules: []MappingRuleConfig{
				{Name: "bad-regex", SourceGroupRegex: `[`, DestinationType: "kubernetes", NameTemplate: "cluster"},
			},
		}

		err := s.loadConfig(config)
		assert.ErrorContains(t, err, "invalid regex")
	})

	t.Run("missing nameTemplate returns error", func(t *testing.T) {
		config := BootstrapConfig{
			MappingRules: []MappingRuleConfig{
				{Name: "no-template", SourceGroupRegex: `platform-.*`, DestinationType: "kubernetes"},
			},
		}

		err := s.loadConfig(config)
		assert.ErrorContains(t, err, "nameTemplate")
	})

	t.Run("missing roleTemplate for kubernetes returns error", func(t *testing.T) {
		config := BootstrapConfig{
			MappingRules: []MappingRuleConfig{
				{Name: "k8s-no-role", SourceGroupRegex: `platform-.*`, DestinationType: "kubernetes", NameTemplate: "cluster"},
			},
		}

		err := s.loadConfig(config)
		assert.Error(t, err, "load mapping rule \"k8s-no-role\": role_template is required for kubernetes mapping rules")
	})

	t.Run("SSH destination works without roleTemplate", func(t *testing.T) {
		config := BootstrapConfig{
			MappingRules: []MappingRuleConfig{
				{Name: "ssh-no-role", SourceGroupRegex: `ssh-.*`, DestinationType: "ssh", NameTemplate: "bastion"},
			},
		}

		err := s.loadConfig(config)
		assert.NilError(t, err)
	})
}

func strPtr(s string) *string {
	return &s
}

func getTestDefaultOrgUserDetails(t *testing.T, server *Server, name string) (*models.Identity, *models.Credential, *models.AccessKey) {
	t.Helper()
	tx := txnForTestCase(t, server.db, server.db.DefaultOrg.ID)

	user, err := data.GetIdentity(tx, data.GetIdentityOptions{ByName: name})
	assert.NilError(t, err, "user")

	credential, err := data.GetCredentialByUserID(tx, user.ID)
	if !errors.Is(err, internal.ErrNotFound) {
		assert.NilError(t, err, "credentials")
	}

	keys, err := data.ListAccessKeys(tx, data.ListAccessKeyOptions{ByIssuedForID: user.ID})
	if !errors.Is(err, internal.ErrNotFound) {
		assert.NilError(t, err, "access_key")
	}

	// only return the first key
	var accessKey *models.AccessKey
	if len(keys) > 0 {
		accessKey = &keys[0]
	}

	return user, credential, accessKey
}
