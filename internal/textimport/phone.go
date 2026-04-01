package textimport

import (
	"fmt"
	"strings"
	"unicode"
)

// NormalizePhone normalizes a phone number to E.164 format.
// Returns an error for inputs that are not phone numbers (emails,
// short codes, system identifiers).
func NormalizePhone(raw string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("empty input")
	}
	// Reject email addresses
	if strings.Contains(raw, "@") {
		return "", fmt.Errorf("not a phone number: %q", raw)
	}

	// Strip all non-digit and non-plus characters
	var b strings.Builder
	for _, r := range raw {
		if r == '+' || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	digits := b.String()

	// Must start with + or be all digits
	if digits == "" {
		return "", fmt.Errorf("no digits in input: %q", raw)
	}

	// Strip leading + for length check
	justDigits := strings.TrimPrefix(digits, "+")
	if len(justDigits) < 7 {
		return "", fmt.Errorf("too short for phone number: %q", raw)
	}

	// Ensure + prefix
	if !strings.HasPrefix(digits, "+") {
		// Assume US country code if 10 digits
		if len(justDigits) == 10 {
			digits = "+1" + justDigits
		} else if len(justDigits) == 11 && justDigits[0] == '1' {
			digits = "+" + justDigits
		} else {
			digits = "+" + justDigits
		}
	}

	return digits, nil
}
