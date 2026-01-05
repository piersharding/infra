package validate

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

type ExampleRequest struct {
	ID string

	Either string
	Or     int

	First  string
	Second int
	Third  bool

	EmailAddr  string
	EmailOther string

	TooFew    string
	TooMany   string
	WrongOnes string

	TooLow  int
	TooHigh int

	Kind string

	Unique string
}

func (r ExampleRequest) ValidationRules() []ValidationRule {
	return []ValidationRule{
		Required("id", r.ID),
		MutuallyExclusive(
			Field{Name: "either", Value: r.Either},
			Field{Name: "or", Value: r.Or},
		),
		RequireAnyOf(
			Field{Name: "first", Value: r.First},
			Field{Name: "second", Value: r.Second},
			Field{Name: "third", Value: r.Third},
		),
		Email("emailAddr", r.EmailAddr),
		Email("emailOther", r.EmailOther),

		StringRule{
			Name:      "tooFew",
			Value:     r.TooFew,
			MinLength: 5,
		},
		StringRule{
			Name:      "tooMany",
			Value:     r.TooMany,
			MaxLength: 5,
		},
		StringRule{
			Name:            "wrongOnes",
			Value:           r.WrongOnes,
			CharacterRanges: []CharRange{AlphabetLower},
		},
		IntRule{Name: "tooLow", Value: r.TooLow, Min: Int(20)},
		IntRule{Name: "tooHigh", Value: r.TooHigh, Max: Int(20)},
		Enum("kind", r.Kind, []string{"fruit", "legume", "grain"}),
		ReservedStrings("unique", r.Unique, []string{"special"}),
	}
}

func TestValidate_AllRules(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		r := ExampleRequest{
			ID:         "id",
			First:      "something",
			EmailAddr:  "valid@example.com",
			EmailOther: "other@example.com",
			TooFew:     "abcdef",
			WrongOnes:  "abc",
			TooLow:     22,
		}
		err := Validate(r)
		assert.NilError(t, err)
	})

	t.Run("with failures", func(t *testing.T) {
		r := ExampleRequest{
			Either:     "yes",
			Or:         1,
			EmailAddr:  "nope~example.com",
			EmailOther: `"Display Name" <other@example.com>`,
			TooFew:     "a",
			TooMany:    "ababab",
			WrongOnes:  "ahCAPS",
			TooLow:     2,
			TooHigh:    22,
			Kind:       "fish",
			Unique:     "special",
		}
		err := Validate(r)
		assert.ErrorContains(t, err, "validation failed: ")

		var fieldError Error
		assert.Assert(t, errors.As(err, &fieldError))
		expected := Error{
			"id": {"is required"},
			"": {
				"only one of (either, or) can have a value",
				"one of (first, second, third) is required",
			},
			"emailAddr":  {"invalid email address"},
			"emailOther": {`email address must not contain display name "Display Name"`},
			"tooFew":     {"must be at least 5 characters"},
			"tooMany":    {"can be at most 5 characters"},
			"wrongOnes":  {"character 'C' at position 2 is not allowed"},
			"tooHigh":    {"value 22 must be at most 20"},
			"tooLow":     {"value 2 must be at least 20"},
			"kind":       {"must be one of (fruit, legume, grain)"},
			"unique":     {"special is reserved and can not be used"},
		}
		assert.DeepEqual(t, fieldError, expected)
	})
}

type NestedExample struct {
	Anything string
	Sub      SubExample `json:"sub"`
	ExampleRequest
	Many []ExampleRequest
}

func (n NestedExample) ValidationRules() []ValidationRule {
	return []ValidationRule{
		StringRule{Name: "any", Value: n.Anything, MaxLength: 3},
	}
}

type SubExample struct {
	Ok     bool
	Nested ExampleRequest `json:"nested"`
}

func (s SubExample) ValidationRules() []ValidationRule {
	return []ValidationRule{
		Required("ok", s.Ok),
	}
}

func TestValidate_Traversal(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		n := NestedExample{
			Sub: SubExample{
				Ok:     true,
				Nested: ExampleRequest{ID: "id", Third: true},
			},
			ExampleRequest: ExampleRequest{ID: "ok", First: "1"},
		}
		err := Validate(n)
		assert.NilError(t, err)
	})

	t.Run("with failures", func(t *testing.T) {
		n := NestedExample{
			Anything: "abcdef",
			Sub: SubExample{
				Nested: ExampleRequest{
					Either: "yes",
					Or:     1,
				},
			},
			ExampleRequest: ExampleRequest{
				ID:     "ok",
				TooFew: "a",
			},
			Many: []ExampleRequest{
				{},
			},
		}
		err := Validate(n)
		assert.ErrorContains(t, err, "validation failed: ")
		var fieldError Error
		assert.Assert(t, errors.As(err, &fieldError))
		expected := Error{
			"":    {"one of (first, second, third) is required"},
			"any": {"can be at most 3 characters"},
			"sub.nested": {
				"only one of (either, or) can have a value",
				"one of (first, second, third) is required",
			},
			"sub.nested.id": {"is required"},
			"sub.ok":        {"is required"},
			"tooFew":        {"must be at least 5 characters"},
			"many":          {"one of (first, second, third) is required"},
			"many.id":       {"is required"},
		}
		assert.DeepEqual(t, fieldError, expected)
	})
}

type MutualExample struct {
	First  string
	Second bool
	Third  int
}

func (m MutualExample) ValidationRules() []ValidationRule {
	return []ValidationRule{
		MutuallyExclusive(
			Field{Name: "first", Value: m.First},
			Field{Name: "second", Value: m.Second},
			Field{Name: "third", Value: m.Third}),
	}
}

func TestMutuallyExclusive_Validate(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		e := MutualExample{First: "value"}
		assert.NilError(t, Validate(e))

		e = MutualExample{}
		assert.NilError(t, Validate(e))

		e = MutualExample{Second: true}
		assert.NilError(t, Validate(e))

		e = MutualExample{Third: 123}
		assert.NilError(t, Validate(e))
	})
	t.Run("with failure two set", func(t *testing.T) {
		e := MutualExample{First: "value", Second: true}
		err := Validate(e)
		assert.Error(t, err, "validation failed: only one of (first, second) can have a value")
	})
	t.Run("with failure three set", func(t *testing.T) {
		e := MutualExample{
			First:  "value",
			Second: true,
			Third:  123,
		}
		err := Validate(e)
		assert.Error(t, err, "validation failed: only one of (first, second, third) can have a value")
	})
}

type AnyOfExample struct {
	First  string
	Second bool
	Third  int
}

func (m AnyOfExample) ValidationRules() []ValidationRule {
	return []ValidationRule{
		RequireAnyOf(
			Field{Name: "first", Value: m.First},
			Field{Name: "second", Value: m.Second},
			Field{Name: "third", Value: m.Third}),
	}
}

func TestRequireAnyOf_Validate(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		e := AnyOfExample{First: "value"}
		assert.NilError(t, Validate(e))

		e = AnyOfExample{Second: true}
		assert.NilError(t, Validate(e))

		e = AnyOfExample{Third: 123}
		assert.NilError(t, Validate(e))
	})
	t.Run("with failure", func(t *testing.T) {
		e := AnyOfExample{}
		err := Validate(e)
		assert.Error(t, err, "validation failed: one of (first, second, third) is required")
	})
}

type OneOfExample struct {
	First  string
	Second bool
	Third  int
}

func (m OneOfExample) ValidationRules() []ValidationRule {
	return []ValidationRule{
		RequireOneOf(
			Field{Name: "first", Value: m.First},
			Field{Name: "second", Value: m.Second},
			Field{Name: "third", Value: m.Third}),
	}
}

func TestRequireOneOf_Validate(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		e := OneOfExample{First: "value"}
		assert.NilError(t, Validate(e))

		e = OneOfExample{Second: true}
		assert.NilError(t, Validate(e))

		e = OneOfExample{Third: 123}
		assert.NilError(t, Validate(e))
	})
	t.Run("with none set", func(t *testing.T) {
		e := OneOfExample{}
		err := Validate(e)
		assert.Error(t, err, "validation failed: one of (first, second, third) is required")
	})
	t.Run("with more than one set", func(t *testing.T) {
		e := OneOfExample{First: "v", Third: 34}
		err := Validate(e)
		assert.Error(t, err, "validation failed: only one of (first, third) can have a value")
	})
}

// TestSecureStringRule tests the enhanced secure string validation
func TestSecureStringRule(t *testing.T) {
	tests := []struct {
		name      string
		value     string
		minLength int
		maxLength int
		pattern   *regexp.Regexp
		wantErr   bool
		errMsg    string
	}{
		{
			name:      "valid string - meets all criteria",
			value:     "validstring123",
			minLength: 5,
			maxLength: 20,
			pattern:   regexp.MustCompile(`^[a-zA-Z0-9]+$`),
			wantErr:   false,
		},
		{
			name:      "too short",
			value:     "abc",
			minLength: 5,
			maxLength: 20,
			pattern:   regexp.MustCompile(`^[a-zA-Z0-9]+$`),
			wantErr:   true,
			errMsg:    "must be at least 5 characters",
		},
		{
			name:      "too long",
			value:     "thisstringiswaytoolongforourlimits",
			minLength: 5,
			maxLength: 20,
			pattern:   regexp.MustCompile(`^[a-zA-Z0-9]+$`),
			wantErr:   true,
			errMsg:    "must not exceed 20 characters",
		},
		{
			name:      "invalid pattern - contains special chars",
			value:     "invalid@string",
			minLength: 5,
			maxLength: 20,
			pattern:   regexp.MustCompile(`^[a-zA-Z0-9]+$`),
			wantErr:   true,
			errMsg:    "contains invalid characters",
		},
		{
			name:      "empty string - valid for optional",
			value:     "",
			minLength: 5,
			maxLength: 20,
			pattern:   regexp.MustCompile(`^[a-zA-Z0-9]+$`),
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule := SecureString(tt.name, tt.value, tt.minLength, tt.maxLength, tt.pattern)
			failure := rule.Validate()

			if tt.wantErr {
				assert.Assert(t, failure != nil)
				assert.Assert(t, failure != nil)
				assert.Assert(t, len(failure.Problems) > 0)
				assert.Assert(t, strings.Contains(failure.Problems[0], tt.errMsg))
			} else {
				assert.Assert(t, failure == nil)
			}
		})
	}
}

// TestSecureEmailRule tests the enhanced email validation
func TestSecureEmailRule(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid email",
			value:   "user@example.com",
			wantErr: false,
		},
		{
			name:    "valid email with subdomain",
			value:   "user@mail.example.com",
			wantErr: false,
		},
		{
			name:    "valid email with numbers",
			value:   "user123@example123.com",
			wantErr: false,
		},
		{
			name:    "invalid email - no @",
			value:   "userexample.com",
			wantErr: true,
			errMsg:  "is not a valid email address",
		},
		{
			name:    "invalid email - no domain",
			value:   "user@",
			wantErr: true,
			errMsg:  "is not a valid email address",
		},
		{
			name:    "invalid email - no TLD",
			value:   "user@example",
			wantErr: true,
			errMsg:  "is not a valid email address",
		},
		{
			name:    "empty string - valid for optional",
			value:   "",
			wantErr: false,
		},
		{
			name:    "invalid email - special chars",
			value:   "user@ex ample.com",
			wantErr: true,
			errMsg:  "is not a valid email address",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule := SecureEmail(tt.name, tt.value)
			failure := rule.Validate()

			if tt.wantErr {
				assert.Assert(t, failure != nil)
				assert.Assert(t, len(failure.Problems) > 0)
				assert.Assert(t, strings.Contains(failure.Problems[0], tt.errMsg))
			} else {
				assert.Assert(t, failure == nil)
			}
		})
	}
}

// TestSecureUsernameRule tests the enhanced username validation
func TestSecureUsernameRule(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid username - letters",
			value:   "alice",
			wantErr: false,
		},
		{
			name:    "valid username - alphanumeric",
			value:   "user123",
			wantErr: false,
		},
		{
			name:    "valid username - with dots",
			value:   "user.name",
			wantErr: false,
		},
		{
			name:    "valid username - with underscores",
			value:   "user_name",
			wantErr: false,
		},
		{
			name:    "valid username - with hyphens",
			value:   "user-name",
			wantErr: false,
		},
		{
			name:    "valid username - exactly 3 chars",
			value:   "abc",
			wantErr: false,
		},
		{
			name:    "valid username - exactly 32 chars",
			value:   "12345678901234567890123456789012",
			wantErr: false,
		},
		{
			name:    "invalid username - too short",
			value:   "ab",
			wantErr: true,
			errMsg:  "must be 3-32 characters and contain only letters, numbers, dots, underscores, or hyphens",
		},
		{
			name:    "invalid username - too long",
			value:   "123456789012345678901234567890123",
			wantErr: true,
			errMsg:  "must be 3-32 characters and contain only letters, numbers, dots, underscores, or hyphens",
		},
		{
			name:    "invalid username - special characters",
			value:   "user@domain",
			wantErr: true,
			errMsg:  "must be 3-32 characters and contain only letters, numbers, dots, underscores, or hyphens",
		},
		{
			name:    "invalid username - spaces",
			value:   "user name",
			wantErr: true,
			errMsg:  "must be 3-32 characters and contain only letters, numbers, dots, underscores, or hyphens",
		},
		{
			name:    "empty string - valid for optional",
			value:   "",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule := SecureUsername(tt.name, tt.value)
			failure := rule.Validate()

			if tt.wantErr {
				assert.Assert(t, failure != nil)
				assert.Assert(t, len(failure.Problems) > 0)
				assert.Assert(t, strings.Contains(failure.Problems[0], tt.errMsg))
			} else {
				assert.Assert(t, failure == nil)
			}
		})
	}
}

// TestPasswordValidation tests the new password validation directly
func TestPasswordValidation(t *testing.T) {
	// Test common password detection with relaxed config (no complexity requirements)
	// This tests the common password check in isolation
	relaxedConfig := RelaxedPasswordConfig()

	result := ValidatePassword("password", relaxedConfig)
	t.Logf("password (relaxed): Valid=%v, Errors=%v", result.Valid, result.Errors)
	assert.Assert(t, !result.Valid, "Common password 'password' should be rejected")
	assert.Assert(t, len(result.Errors) > 0, "Should have at least one error")
	assert.Assert(t, strings.Contains(result.Errors[0], "commonly used password"), "Error should mention common password")

	result2 := ValidatePassword("qwerty123", relaxedConfig)
	t.Logf("qwerty123 (relaxed): Valid=%v, Errors=%v", result2.Valid, result2.Errors)
	assert.Assert(t, !result2.Valid, "Common password 'qwerty123' should be rejected")

	// Test a password that meets all requirements with default config
	result3 := ValidatePassword("MyStr0ngP@ssw0rd", DefaultPasswordConfig())
	t.Logf("MyStr0ngP@ssw0rd: Valid=%v, Errors=%v", result3.Valid, result3.Errors)
	assert.Assert(t, result3.Valid, "Strong password should be valid")

	// Test that complexity requirements are enforced with default config
	result4 := ValidatePassword("password", DefaultPasswordConfig())
	t.Logf("password (default): Valid=%v, Errors=%v", result4.Valid, result4.Errors)
	assert.Assert(t, !result4.Valid, "Simple password should fail default config")
}

// TestSecurePasswordRule tests the enhanced password validation
func TestSecurePasswordRule(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid password - meets all criteria",
			value:   "MyStr0ngP@ssw0rd",
			wantErr: false,
		},
		{
			name:    "valid password - minimum length",
			value:   "Aa1!5678",
			wantErr: false,
		},
		{
			name:    "valid password - with special chars",
			value:   "P@ssw0rd123!",
			wantErr: false,
		},
		{
			name:    "too short password",
			value:   "1234567",
			wantErr: true,
			errMsg:  "must be at least 8 characters",
		},
		{
			name:    "password without uppercase",
			value:   "mypassword123!",
			wantErr: true,
			errMsg:  "must contain at least one uppercase letter",
		},
		{
			name:    "password without lowercase",
			value:   "MYPASSWORD123!",
			wantErr: true,
			errMsg:  "must contain at least one lowercase letter",
		},
		{
			name:    "password without numbers",
			value:   "MyPassword!!",
			wantErr: true,
			errMsg:  "must contain at least one number",
		},
		{
			name:    "password without special characters",
			value:   "MyPassword123",
			wantErr: true,
			errMsg:  "must contain at least one special character",
		},
		{
			name:    "empty password - valid for optional",
			value:   "",
			wantErr: false,
		},
		{
			name:    "password exceeding max length",
			value:   "ThisIsAVeryLongPasswordThatExceedsTheMaximumLengthAllowedByTheSystemAndShouldFailValidation1234567890!@#$%^&*()ThisIsAVeryLongPasswordThatExceeds",
			wantErr: true,
			errMsg:  "must not exceed 128 characters",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule := SecurePassword(tt.name, tt.value)
			failure := rule.Validate()

			if tt.wantErr {
				assert.Assert(t, failure != nil)
				assert.Assert(t, len(failure.Problems) > 0)
				assert.Assert(t, strings.Contains(failure.Problems[0], tt.errMsg))
			} else {
				assert.Assert(t, failure == nil)
			}
		})
	}
}

// UserRegistrationRequest is a test struct for integration testing
type UserRegistrationRequest struct {
	Username string
	Email    string
	Password string
	Bio      string
}

func (r UserRegistrationRequest) ValidationRules() []ValidationRule {
	return []ValidationRule{
		SecureUsername("username", r.Username),
		SecureEmail("email", r.Email),
		SecurePassword("password", r.Password),
		SecureString("bio", r.Bio, 10, 500, regexp.MustCompile(`^[a-zA-Z0-9 .,!?:;()-]+$`)),
	}
}

// TestEnhancedValidationIntegration tests the integration of enhanced validation rules
func TestEnhancedValidationIntegration(t *testing.T) {
	t.Run("valid registration", func(t *testing.T) {
		req := UserRegistrationRequest{
			Username: "alice",
			Email:    "alice@example.com",
			Password: "MyStr0ngP@ssw0rd",
			Bio:      "I am a software developer with 5 years of experience.",
		}
		err := Validate(req)
		assert.NilError(t, err)
	})

	t.Run("invalid registration - multiple errors", func(t *testing.T) {
		req := UserRegistrationRequest{
			Username: "ab",            // too short
			Email:    "invalid-email", // invalid format
			Password: "weak",          // too short and doesn't meet complexity
			Bio:      "short",         // too short
		}
		err := Validate(req)
		assert.ErrorContains(t, err, "validation failed: ")

		var fieldError Error
		assert.Assert(t, errors.As(err, &fieldError))

		assert.Assert(t, len(fieldError) > 0)
		assert.Assert(t, len(fieldError["username"]) > 0)
		assert.Assert(t, strings.Contains(fieldError["username"][0], "must be 3-32 characters"))
		assert.Assert(t, len(fieldError["email"]) > 0)
		assert.Assert(t, strings.Contains(fieldError["email"][0], "is not a valid email address"))
		assert.Assert(t, len(fieldError["password"]) > 0)
		assert.Assert(t, strings.Contains(fieldError["password"][0], "must be at least 8 characters"))
		assert.Assert(t, len(fieldError["bio"]) > 0)
		assert.Assert(t, strings.Contains(fieldError["bio"][0], "must be at least 10 characters"))
	})
}
