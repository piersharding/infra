package validate

import (
	_ "embed"
	"regexp"
	"strings"
	"unicode"

	"github.com/infrahq/infra/internal/openapi3"
)

// PasswordConfig holds configuration for password validation
type PasswordConfig struct {
	// MinLength is the minimum password length (default: 8)
	MinLength int

	// MaxLength is the maximum password length (default: 128)
	MaxLength int

	// RequireUppercase requires at least one uppercase letter
	RequireUppercase bool

	// RequireLowercase requires at least one lowercase letter
	RequireLowercase bool

	// RequireNumber requires at least one digit
	RequireNumber bool

	// RequireSpecial requires at least one special character
	RequireSpecial bool

	// CheckCommonPasswords enables checking against the embedded common passwords list
	CheckCommonPasswords bool

	// CustomBadPasswords is an optional list of additional passwords to reject
	CustomBadPasswords []string
}

// DefaultPasswordConfig returns the default password configuration with
// industry-standard security requirements
func DefaultPasswordConfig() PasswordConfig {
	return PasswordConfig{
		MinLength:            8,
		MaxLength:            128,
		RequireUppercase:     true,
		RequireLowercase:     true,
		RequireNumber:        true,
		RequireSpecial:       true,
		CheckCommonPasswords: true,
	}
}

// RelaxedPasswordConfig returns a less strict configuration suitable for
// environments where password complexity requirements need to be reduced
func RelaxedPasswordConfig() PasswordConfig {
	return PasswordConfig{
		MinLength:            8,
		MaxLength:            128,
		RequireUppercase:     false,
		RequireLowercase:     false,
		RequireNumber:        false,
		RequireSpecial:       false,
		CheckCommonPasswords: true,
	}
}

// commonPasswords contains a list of the most commonly used passwords.
// These passwords are trivially guessable and should never be allowed.
// Source: Various security research including NCSC, Have I Been Pwned, etc.
//
//go:embed common_passwords.txt
var commonPasswordsData string

// commonPasswordsSet is a set for O(1) lookup of common passwords
var commonPasswordsSet map[string]struct{}

func init() {
	commonPasswordsSet = make(map[string]struct{})
	for _, line := range strings.Split(commonPasswordsData, "\n") {
		password := strings.TrimSpace(strings.ToLower(line))
		if password != "" && !strings.HasPrefix(password, "#") {
			commonPasswordsSet[password] = struct{}{}
		}
	}
}

// PasswordValidationResult contains the result of password validation
type PasswordValidationResult struct {
	Valid    bool
	Errors   []string
	Warnings []string
}

// ValidatePassword validates a password against the given configuration
func ValidatePassword(password string, config PasswordConfig) PasswordValidationResult {
	result := PasswordValidationResult{Valid: true}

	// Check minimum length
	if len(password) < config.MinLength {
		result.Valid = false
		result.Errors = append(result.Errors, "must be at least "+intToStr(config.MinLength)+" characters")
	}

	// Check maximum length
	if len(password) > config.MaxLength {
		result.Valid = false
		result.Errors = append(result.Errors, "must not exceed "+intToStr(config.MaxLength)+" characters")
	}

	// Check character requirements
	var (
		hasUpper   bool
		hasLower   bool
		hasNumber  bool
		hasSpecial bool
	)

	for _, char := range password {
		switch {
		case unicode.IsUpper(char):
			hasUpper = true
		case unicode.IsLower(char):
			hasLower = true
		case unicode.IsDigit(char):
			hasNumber = true
		case isSpecialChar(char):
			hasSpecial = true
		}
	}

	if config.RequireUppercase && !hasUpper {
		result.Valid = false
		result.Errors = append(result.Errors, "must contain at least one uppercase letter")
	}

	if config.RequireLowercase && !hasLower {
		result.Valid = false
		result.Errors = append(result.Errors, "must contain at least one lowercase letter")
	}

	if config.RequireNumber && !hasNumber {
		result.Valid = false
		result.Errors = append(result.Errors, "must contain at least one number")
	}

	if config.RequireSpecial && !hasSpecial {
		result.Valid = false
		result.Errors = append(result.Errors, "must contain at least one special character")
	}

	// Check against common passwords
	if config.CheckCommonPasswords {
		if isCommonPassword(password) {
			result.Valid = false
			result.Errors = append(result.Errors, "cannot use a commonly used password")
		}
	}

	// Check against custom bad passwords
	if len(config.CustomBadPasswords) > 0 {
		lowerPassword := strings.ToLower(password)
		for _, bad := range config.CustomBadPasswords {
			if strings.ToLower(bad) == lowerPassword {
				result.Valid = false
				result.Errors = append(result.Errors, "password is not allowed")
				break
			}
		}
	}

	// Add warnings for weak but technically valid passwords
	if result.Valid {
		if len(password) < 12 {
			result.Warnings = append(result.Warnings, "consider using a longer password for better security")
		}
		if isSequentialOrRepeating(password) {
			result.Warnings = append(result.Warnings, "password contains sequential or repeating patterns")
		}
	}

	return result
}

// ValidatePasswordDefault validates a password with the default configuration
func ValidatePasswordDefault(password string) PasswordValidationResult {
	return ValidatePassword(password, DefaultPasswordConfig())
}

// IsValidPassword returns true if the password meets the default requirements
func IsValidPassword(password string) bool {
	return ValidatePasswordDefault(password).Valid
}

// isCommonPassword checks if the password is in the common passwords list
func isCommonPassword(password string) bool {
	_, found := commonPasswordsSet[strings.ToLower(password)]
	return found
}

// isSpecialChar returns true if the character is a special character
func isSpecialChar(r rune) bool {
	specialChars := "!@#$%^&*()_+-=[]{}|;':\",./<>?`~"
	return strings.ContainsRune(specialChars, r)
}

// isSequentialOrRepeating checks for sequential or repeating patterns
func isSequentialOrRepeating(password string) bool {
	if len(password) < 3 {
		return false
	}

	// Check for repeating characters (e.g., "aaa", "111")
	repeatCount := 1
	for i := 1; i < len(password); i++ {
		if password[i] == password[i-1] {
			repeatCount++
			if repeatCount >= 3 {
				return true
			}
		} else {
			repeatCount = 1
		}
	}

	// Check for sequential characters (e.g., "abc", "123")
	seqCount := 1
	for i := 1; i < len(password); i++ {
		if password[i] == password[i-1]+1 || password[i] == password[i-1]-1 {
			seqCount++
			if seqCount >= 4 {
				return true
			}
		} else {
			seqCount = 1
		}
	}

	return false
}

// intToStr converts an int to string without importing strconv
func intToStr(n int) string {
	if n == 0 {
		return "0"
	}
	if n < 0 {
		return "-" + intToStr(-n)
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// PasswordRule is a ValidationRule that validates passwords with the default config
type PasswordRule struct {
	name   string
	value  string
	config PasswordConfig
}

// Password creates a validation rule for passwords with the default configuration
func Password(name string, value string) ValidationRule {
	return PasswordRule{
		name:   name,
		value:  value,
		config: DefaultPasswordConfig(),
	}
}

// PasswordWithConfig creates a validation rule for passwords with a custom configuration
func PasswordWithConfig(name string, value string, config PasswordConfig) ValidationRule {
	return PasswordRule{
		name:   name,
		value:  value,
		config: config,
	}
}

// Validate implements ValidationRule
func (r PasswordRule) Validate() *Failure {
	if r.value == "" {
		return nil // Let Required rule handle this
	}

	result := ValidatePassword(r.value, r.config)
	if !result.Valid && len(result.Errors) > 0 {
		return Fail(r.name, result.Errors[0])
	}
	return nil
}

// DescribeSchema implements ValidationRule for OpenAPI documentation
func (r PasswordRule) DescribeSchema(schema *openapi3.Schema) {
	property := schemaForProperty(schema, r.name)
	property.MinLength = uint64(r.config.MinLength)
	maxLen := uint64(r.config.MaxLength)
	property.MaxLength = &maxLen
	property.Format = "password"

	// Add pattern hint for documentation
	var hints []string
	if r.config.RequireUppercase {
		hints = append(hints, "uppercase letter")
	}
	if r.config.RequireLowercase {
		hints = append(hints, "lowercase letter")
	}
	if r.config.RequireNumber {
		hints = append(hints, "number")
	}
	if r.config.RequireSpecial {
		hints = append(hints, "special character")
	}
	if len(hints) > 0 {
		property.Description = "Password must contain at least one " + strings.Join(hints, ", ")
	}
}

// EstimatePasswordStrength returns a rough estimate of password strength (0-4)
// 0 = Very Weak, 1 = Weak, 2 = Fair, 3 = Strong, 4 = Very Strong
func EstimatePasswordStrength(password string) int {
	if len(password) == 0 {
		return 0
	}

	score := 0

	// Length scoring
	switch {
	case len(password) >= 16:
		score += 2
	case len(password) >= 12:
		score += 1
	}

	// Character variety scoring
	var hasUpper, hasLower, hasNumber, hasSpecial bool
	for _, char := range password {
		switch {
		case unicode.IsUpper(char):
			hasUpper = true
		case unicode.IsLower(char):
			hasLower = true
		case unicode.IsDigit(char):
			hasNumber = true
		case isSpecialChar(char):
			hasSpecial = true
		}
	}

	varietyCount := 0
	if hasUpper {
		varietyCount++
	}
	if hasLower {
		varietyCount++
	}
	if hasNumber {
		varietyCount++
	}
	if hasSpecial {
		varietyCount++
	}

	score += varietyCount / 2

	// Penalize common passwords
	if isCommonPassword(password) {
		return 0
	}

	// Penalize sequential/repeating patterns
	if isSequentialOrRepeating(password) {
		score--
	}

	// Clamp to 0-4 range
	if score < 0 {
		return 0
	}
	if score > 4 {
		return 4
	}
	return score
}

// PasswordStrengthLabel returns a human-readable label for the strength score
func PasswordStrengthLabel(strength int) string {
	switch strength {
	case 0:
		return "Very Weak"
	case 1:
		return "Weak"
	case 2:
		return "Fair"
	case 3:
		return "Strong"
	case 4:
		return "Very Strong"
	default:
		return "Unknown"
	}
}

// Compile-time check for common password patterns
var (
	keyboardPatterns = regexp.MustCompile(`(?i)(qwerty|asdf|zxcv|qazwsx|1qaz|2wsx)`)
	datePattern      = regexp.MustCompile(`(19|20)\d{2}(0[1-9]|1[012])(0[1-9]|[12][0-9]|3[01])`)
)

// HasKeyboardPattern checks if the password contains common keyboard patterns
func HasKeyboardPattern(password string) bool {
	return keyboardPatterns.MatchString(password)
}

// HasDatePattern checks if the password contains a date pattern
func HasDatePattern(password string) bool {
	return datePattern.MatchString(password)
}
