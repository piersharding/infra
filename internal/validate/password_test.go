package validate

import (
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

func TestValidatePassword(t *testing.T) {
	t.Run("default config - valid passwords", func(t *testing.T) {
		validPasswords := []string{
			"MyStr0ng!Pass",
			"P@ssw0rd123!",
			"Abc123!@#xyz",
			"SuperS3cur3!",
		}

		for _, password := range validPasswords {
			t.Run(password, func(t *testing.T) {
				result := ValidatePassword(password, DefaultPasswordConfig())
				assert.Assert(t, result.Valid, "password should be valid: %s, errors: %v", password, result.Errors)
			})
		}
	})

	t.Run("default config - invalid passwords", func(t *testing.T) {
		testCases := []struct {
			password string
			errMsg   string
		}{
			{"short", "must be at least 8 characters"},
			{"nouppercase123!", "must contain at least one uppercase letter"},
			{"NOLOWERCASE123!", "must contain at least one lowercase letter"},
			{"NoNumbers!!", "must contain at least one number"},
			{"NoSpecial123", "must contain at least one special character"},
		}

		for _, tc := range testCases {
			t.Run(tc.password, func(t *testing.T) {
				result := ValidatePassword(tc.password, DefaultPasswordConfig())
				assert.Assert(t, !result.Valid, "password should be invalid")
				found := false
				for _, err := range result.Errors {
					if strings.Contains(err, tc.errMsg) {
						found = true
						break
					}
				}
				assert.Assert(t, found, "expected error containing %q in %v", tc.errMsg, result.Errors)
			})
		}
	})

	t.Run("max length exceeded", func(t *testing.T) {
		longPassword := strings.Repeat("Aa1!", 50) // 200 characters
		result := ValidatePassword(longPassword, DefaultPasswordConfig())
		assert.Assert(t, !result.Valid)
		assert.Assert(t, len(result.Errors) > 0)
		assert.Assert(t, strings.Contains(result.Errors[0], "must not exceed 128 characters"))
	})

	t.Run("relaxed config - no complexity required", func(t *testing.T) {
		config := RelaxedPasswordConfig()
		// "password" is in the common passwords list
		result := ValidatePassword("password", config)
		// Should fail only because it's a common password
		assert.Assert(t, !result.Valid)
		assert.Assert(t, strings.Contains(result.Errors[0], "commonly used password"))

		// Non-common simple password should pass with relaxed config
		result2 := ValidatePassword("notcommon123", config)
		assert.Assert(t, result2.Valid)
	})

	t.Run("custom config", func(t *testing.T) {
		config := PasswordConfig{
			MinLength:            10,
			MaxLength:            20,
			RequireUppercase:     true,
			RequireLowercase:     false,
			RequireNumber:        false,
			RequireSpecial:       false,
			CheckCommonPasswords: false,
		}

		// Too short
		result := ValidatePassword("ABCDEFGHI", config)
		assert.Assert(t, !result.Valid)
		assert.Assert(t, strings.Contains(result.Errors[0], "must be at least 10 characters"))

		// Valid with config
		result2 := ValidatePassword("ABCDEFGHIJ", config)
		assert.Assert(t, result2.Valid)

		// Too long
		result3 := ValidatePassword("ABCDEFGHIJKLMNOPQRSTUVWXYZ", config)
		assert.Assert(t, !result3.Valid)
		assert.Assert(t, strings.Contains(result3.Errors[0], "must not exceed 20 characters"))
	})

	t.Run("custom bad passwords", func(t *testing.T) {
		config := RelaxedPasswordConfig()
		config.CustomBadPasswords = []string{"mycompanyname", "secretproject"}

		result := ValidatePassword("mycompanyname", config)
		assert.Assert(t, !result.Valid)
		assert.Assert(t, strings.Contains(result.Errors[0], "password is not allowed"))

		result2 := ValidatePassword("SecretProject", config) // case insensitive
		assert.Assert(t, !result2.Valid)
	})

	t.Run("warnings for weak but valid passwords", func(t *testing.T) {
		config := DefaultPasswordConfig()
		// Valid but short
		result := ValidatePassword("Aa1!5678", config)
		assert.Assert(t, result.Valid)
		assert.Assert(t, len(result.Warnings) > 0)
		found := false
		for _, w := range result.Warnings {
			if strings.Contains(w, "longer password") {
				found = true
				break
			}
		}
		assert.Assert(t, found, "expected warning about length")
	})
}

func TestCommonPasswordCheck(t *testing.T) {
	testCases := []struct {
		password string
		isCommon bool
	}{
		{"password", true},
		{"123456", true},
		{"qwerty", true},
		{"letmein", true},
		{"admin", true},
		{"welcome", true},
		{"MyUniqueP@ss123", false},
		{"notinlist", false},
		{"xK9#mN2$pL5@", false},
	}

	for _, tc := range testCases {
		t.Run(tc.password, func(t *testing.T) {
			result := isCommonPassword(tc.password)
			assert.Equal(t, result, tc.isCommon)
		})
	}

	t.Run("case insensitive", func(t *testing.T) {
		assert.Assert(t, isCommonPassword("PASSWORD"))
		assert.Assert(t, isCommonPassword("Password"))
		assert.Assert(t, isCommonPassword("QWERTY"))
	})
}

func TestIsSequentialOrRepeating(t *testing.T) {
	testCases := []struct {
		password   string
		isSequence bool
	}{
		{"aaa", true},
		{"1111", true},
		{"abcd", true},
		{"1234", true},
		{"dcba", true},
		{"4321", true},
		{"abc", false}, // only 3 chars, needs 4 for sequential
		{"aa", false},  // only 2 chars, needs 3 for repeating
		{"random", false},
		{"MyP@ss", false},
	}

	for _, tc := range testCases {
		t.Run(tc.password, func(t *testing.T) {
			result := isSequentialOrRepeating(tc.password)
			assert.Equal(t, result, tc.isSequence)
		})
	}
}

func TestPasswordStrength(t *testing.T) {
	testCases := []struct {
		password string
		minScore int
		maxScore int
	}{
		{"password", 0, 0},            // common password
		{"short", 0, 1},               // too short, weak
		{"longerpassword", 1, 2},      // longer but no variety
		{"Longer123", 1, 2},           // has some variety but short
		{"MyStr0ng!P@ss", 2, 4},       // good variety
		{"VeryL0ng!P@ssw0rd#$", 3, 4}, // excellent
	}

	for _, tc := range testCases {
		t.Run(tc.password, func(t *testing.T) {
			score := EstimatePasswordStrength(tc.password)
			assert.Assert(t, score >= tc.minScore && score <= tc.maxScore,
				"password %q: expected score %d-%d, got %d", tc.password, tc.minScore, tc.maxScore, score)
		})
	}
}

func TestPasswordStrengthLabel(t *testing.T) {
	testCases := []struct {
		score int
		label string
	}{
		{0, "Very Weak"},
		{1, "Weak"},
		{2, "Fair"},
		{3, "Strong"},
		{4, "Very Strong"},
		{-1, "Unknown"},
		{5, "Unknown"},
	}

	for _, tc := range testCases {
		t.Run(tc.label, func(t *testing.T) {
			result := PasswordStrengthLabel(tc.score)
			assert.Equal(t, result, tc.label)
		})
	}
}

func TestHasKeyboardPattern(t *testing.T) {
	testCases := []struct {
		password   string
		hasPattern bool
	}{
		{"qwerty123", true},
		{"myasdfpassword", true},
		{"1qaz2wsx", true},
		{"normalpassword", false},
		{"randomchars", false},
	}

	for _, tc := range testCases {
		t.Run(tc.password, func(t *testing.T) {
			result := HasKeyboardPattern(tc.password)
			assert.Equal(t, result, tc.hasPattern)
		})
	}
}

func TestHasDatePattern(t *testing.T) {
	testCases := []struct {
		password   string
		hasPattern bool
	}{
		{"pass19900515word", true},
		{"20231225password", true},
		{"password", false},
		{"12345678", false},
	}

	for _, tc := range testCases {
		t.Run(tc.password, func(t *testing.T) {
			result := HasDatePattern(tc.password)
			assert.Equal(t, result, tc.hasPattern)
		})
	}
}

func TestPasswordRule(t *testing.T) {
	t.Run("valid password", func(t *testing.T) {
		rule := Password("password", "MyStr0ng!Pass")
		failure := rule.Validate()
		assert.Assert(t, failure == nil)
	})

	t.Run("invalid password", func(t *testing.T) {
		rule := Password("password", "weak")
		failure := rule.Validate()
		assert.Assert(t, failure != nil)
		assert.Assert(t, len(failure.Problems) > 0)
	})

	t.Run("empty password - let Required handle it", func(t *testing.T) {
		rule := Password("password", "")
		failure := rule.Validate()
		assert.Assert(t, failure == nil)
	})

	t.Run("with custom config", func(t *testing.T) {
		config := RelaxedPasswordConfig()
		rule := PasswordWithConfig("password", "simplepass", config)
		failure := rule.Validate()
		assert.Assert(t, failure == nil)
	})
}

func TestValidatePasswordDefault(t *testing.T) {
	result := ValidatePasswordDefault("MyStr0ng!Pass")
	assert.Assert(t, result.Valid)

	result2 := ValidatePasswordDefault("weak")
	assert.Assert(t, !result2.Valid)
}

func TestIsValidPassword(t *testing.T) {
	assert.Assert(t, IsValidPassword("MyStr0ng!Pass"))
	assert.Assert(t, !IsValidPassword("weak"))
}

func TestIntToStr(t *testing.T) {
	testCases := []struct {
		input    int
		expected string
	}{
		{0, "0"},
		{1, "1"},
		{10, "10"},
		{123, "123"},
		{-1, "-1"},
		{-123, "-123"},
	}

	for _, tc := range testCases {
		t.Run(tc.expected, func(t *testing.T) {
			result := intToStr(tc.input)
			assert.Equal(t, result, tc.expected)
		})
	}
}
