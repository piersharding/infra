package linux

import (
	"os"
	"testing"

	"github.com/infrahq/infra/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateUsername(t *testing.T) {
	tests := []struct {
		name     string
		username string
		wantErr  bool
	}{
		{
			name:     "valid username - letters only",
			username: "alice",
			wantErr:  false,
		},
		{
			name:     "valid username - alphanumeric",
			username: "user123",
			wantErr:  false,
		},
		{
			name:     "valid username - with dots",
			username: "user.name",
			wantErr:  false,
		},
		{
			name:     "valid username - with underscores",
			username: "user_name",
			wantErr:  false,
		},
		{
			name:     "valid username - with hyphens",
			username: "user-name",
			wantErr:  false,
		},
		{
			name:     "valid username - exactly 32 chars",
			username: "12345678901234567890123456789012",
			wantErr:  false,
		},
		{
			name:     "invalid username - too short",
			username: "",
			wantErr:  true,
		},
		{
			name:     "invalid username - too long",
			username: "123456789012345678901234567890123",
			wantErr:  true,
		},
		{
			name:     "invalid username - special characters",
			username: "user@domain",
			wantErr:  true,
		},
		{
			name:     "invalid username - spaces",
			username: "user name",
			wantErr:  true,
		},
		{
			name:     "valid username - uppercase letters",
			username: "USER",
			wantErr:  false,
		},
		{
			name:     "invalid username - special symbols",
			username: "user$",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateUsername(tt.username)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "invalid username format")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestSanitizeUsernameForLogging(t *testing.T) {
	tests := []struct {
		name     string
		username string
		expected string
	}{
		{
			name:     "long username",
			username: "alice",
			expected: "a***e",
		},
		{
			name:     "very long username",
			username: "verylongusername123",
			expected: "v***3",
		},
		{
			name:     "short username",
			username: "ab",
			expected: "***",
		},
		{
			name:     "single character",
			username: "a",
			expected: "***",
		},
		{
			name:     "empty username",
			username: "",
			expected: "***",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeUsernameForLogging(tt.username)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestAddUser_Validation(t *testing.T) {
	tests := []struct {
		name    string
		user    *api.User
		group   string
		wantErr bool
	}{
		{
			name:    "valid user",
			user:    &api.User{SSHLoginName: "alice"},
			group:   "users",
			wantErr: false,
		},
		{
			name:    "invalid username - too long",
			user:    &api.User{SSHLoginName: "123456789012345678901234567890123"},
			group:   "users",
			wantErr: true,
		},
		{
			name:    "invalid username - special characters",
			user:    &api.User{SSHLoginName: "user@domain"},
			group:   "users",
			wantErr: true,
		},
		{
			name:    "invalid username - empty",
			user:    &api.User{SSHLoginName: ""},
			group:   "users",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := AddUser(tt.user, tt.group)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "invalid username")
			} else {
				// We expect this to fail due to command execution, but not due to validation
				assert.Error(t, err)
				assert.NotContains(t, err.Error(), "invalid username")
			}
		})
	}
}

func TestKillUserProcesses_Validation(t *testing.T) {
	tests := []struct {
		name    string
		user    LocalUser
		wantErr bool
	}{
		{
			name:    "valid user",
			user:    LocalUser{Username: "alice"},
			wantErr: false,
		},
		{
			name:    "invalid username - too long",
			user:    LocalUser{Username: "123456789012345678901234567890123"},
			wantErr: true,
		},
		{
			name:    "invalid username - special characters",
			user:    LocalUser{Username: "user@domain"},
			wantErr: true,
		},
		{
			name:    "invalid username - empty",
			user:    LocalUser{Username: ""},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := KillUserProcesses(tt.user)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "invalid username")
			} else {
				// We expect this to fail due to command execution, but not due to validation
				assert.Error(t, err)
				assert.NotContains(t, err.Error(), "invalid username")
			}
		})
	}
}

func TestRemoveUser_Validation(t *testing.T) {
	tests := []struct {
		name    string
		user    LocalUser
		wantErr bool
	}{
		{
			name:    "valid user",
			user:    LocalUser{Username: "alice"},
			wantErr: false,
		},
		{
			name:    "invalid username - too long",
			user:    LocalUser{Username: "123456789012345678901234567890123"},
			wantErr: true,
		},
		{
			name:    "invalid username - special characters",
			user:    LocalUser{Username: "user@domain"},
			wantErr: true,
		},
		{
			name:    "invalid username - empty",
			user:    LocalUser{Username: ""},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := RemoveUser(tt.user)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "invalid username")
			} else {
				// We expect this to fail due to command execution, but not due to validation
				// or succeed if user doesn't exist (which is valid)
				// Just check that it's not a validation error
				if err != nil {
					assert.NotContains(t, err.Error(), "invalid username")
				}
			}
		})
	}
}

func TestIsManagedByInfra(t *testing.T) {
	tests := []struct {
		name     string
		user     LocalUser
		expected bool
	}{
		{
			name: "managed by infra",
			user: LocalUser{
				Username: "alice",
				Info:     []string{"alice", sentinelManagedByInfra},
			},
			expected: true,
		},
		{
			name: "not managed by infra",
			user: LocalUser{
				Username: "alice",
				Info:     []string{"alice", "regular user"},
			},
			expected: false,
		},
		{
			name: "empty info",
			user: LocalUser{
				Username: "alice",
				Info:     []string{},
			},
			expected: false,
		},
		{
			name: "single info field",
			user: LocalUser{
				Username: "alice",
				Info:     []string{"alice"},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.user.IsManagedByInfra()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestReadLocalUsers(t *testing.T) {
	// Test with valid passwd format
	passwdContent := `root:x:0:0:root:/root:/bin/bash
daemon:x:1:1:daemon:/usr/sbin:/usr/sbin/nologin
alice:x:1000:1000:Alice,,managed by infra:/home/alice:/bin/bash
bob:x:1001:1001:Bob,managed by infra:/home/bob:/bin/bash
`

	// Write test content to temporary file
	tmpFile, err := os.CreateTemp("", "passwd-test-*")
	require.NoError(t, err)
	defer os.Remove(tmpFile.Name())

	_, err = tmpFile.WriteString(passwdContent)
	require.NoError(t, err)
	tmpFile.Close()

	users, err := ReadLocalUsers(tmpFile.Name())
	require.NoError(t, err)

	assert.Equal(t, 4, len(users))

	// Check first user
	assert.Equal(t, "root", users[0].Username)
	assert.Equal(t, "0", users[0].UID)
	assert.Equal(t, "0", users[0].GID)
	assert.Equal(t, []string{"root"}, users[0].Info)
	assert.Equal(t, "/root", users[0].HomeDir)

	// Check alice user
	assert.Equal(t, "alice", users[2].Username)
	assert.Equal(t, "1000", users[2].UID)
	assert.Equal(t, "1000", users[2].GID)
	assert.Equal(t, []string{"Alice", "managed by infra"}, users[2].Info)
	assert.Equal(t, "/home/alice", users[2].HomeDir)

	// Check bob user
	assert.Equal(t, "bob", users[3].Username)
	assert.Equal(t, "1001", users[3].UID)
	assert.Equal(t, "1001", users[3].GID)
	assert.Equal(t, []string{"Bob", "managed by infra"}, users[3].Info)
	assert.Equal(t, "/home/bob", users[3].HomeDir)
}

func TestReadLocalUsers_InvalidFormat(t *testing.T) {
	// Test with invalid passwd format (too few fields)
	invalidContent := `root:x:0:0:root:/root
daemon:x:1:1:daemon:/usr/sbin:/usr/sbin/nologin
`

	// Write test content to temporary file
	tmpFile, err := os.CreateTemp("", "passwd-test-invalid-*")
	require.NoError(t, err)
	defer os.Remove(tmpFile.Name())

	_, err = tmpFile.WriteString(invalidContent)
	require.NoError(t, err)
	tmpFile.Close()

	users, err := ReadLocalUsers(tmpFile.Name())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid line contains less than 7 fields")
	assert.Nil(t, users)
}

func TestReadLocalUsers_EmptyFile(t *testing.T) {
	// Test with empty file
	tmpFile, err := os.CreateTemp("", "passwd-test-empty-*")
	require.NoError(t, err)
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	users, err := ReadLocalUsers(tmpFile.Name())
	require.NoError(t, err)
	assert.Equal(t, 0, len(users))
}

func TestReadLocalUsers_CommentsAndEmptyLines(t *testing.T) {
	// Test with comments and empty lines
	content := `# This is a comment
root:x:0:0:root:/root:/bin/bash

daemon:x:1:1:daemon:/usr/sbin:/usr/sbin/nologin
# Another comment
alice:x:1000:1000:Alice,,managed by infra:/home/alice:/bin/bash

`

	// Write test content to temporary file
	tmpFile, err := os.CreateTemp("", "passwd-test-comments-*")
	require.NoError(t, err)
	defer os.Remove(tmpFile.Name())

	_, err = tmpFile.WriteString(content)
	require.NoError(t, err)
	tmpFile.Close()

	users, err := ReadLocalUsers(tmpFile.Name())
	require.NoError(t, err)
	assert.Equal(t, 3, len(users))
	assert.Equal(t, "root", users[0].Username)
	assert.Equal(t, "daemon", users[1].Username)
	assert.Equal(t, "alice", users[2].Username)
}
