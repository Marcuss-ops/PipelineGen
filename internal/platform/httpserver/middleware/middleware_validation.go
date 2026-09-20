package middleware

import (
	"strings"

	"github.com/go-playground/validator/v10"
)

// MaxBodySize is the default maximum request body size (1MB).
const MaxBodySize = 1 << 20 // 1MB

// validate is a shared validator instance.
var validate = validator.New()

// BindAndValidate binds JSON from the request body into dst, limits body size,
// and runs struct validation. Returns a user-friendly error on failure.

// ValidationError represents a structured validation error.
type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// friendlyValidationMessage converts validator tags to user-friendly messages.
func friendlyValidationMessage(fe validator.FieldError) string {
	switch fe.Tag() {
	case "required":
		return fe.Field() + " is required"
	case "url":
		return fe.Field() + " must be a valid URL"
	case "gte":
		return fe.Field() + " must be at least " + fe.Param()
	case "lte":
		return fe.Field() + " must be at most " + fe.Param()
	case "min":
		return fe.Field() + " must have at least " + fe.Param() + " items"
	case "max":
		return fe.Field() + " must have at most " + fe.Param() + " items"
	case "oneof":
		return fe.Field() + " must be one of: " + fe.Param()
	default:
		return fe.Field() + " failed " + fe.Tag() + " validation"
	}
}

// SanitizeString trims whitespace and removes control characters from a string.
// Useful for user-supplied text that will be logged or stored.
func SanitizeString(s string) string {
	s = strings.TrimSpace(s)
	// Remove control characters (except common whitespace)
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r >= 32 || r == '\n' || r == '\r' || r == '\t' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// MaxBytesMiddleware is a Gin middleware that limits request body size.
