package middleware

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
)

// MaxBodySize is the default maximum request body size (1MB).
const MaxBodySize = 1 << 20 // 1MB

// validate is a shared validator instance.
var validate = validator.New()

// BindAndValidate binds JSON from the request body into dst, limits body size,
// and runs struct validation. Returns a user-friendly error on failure.
func BindAndValidate(c *gin.Context, dst any) error {
	// Limit request body size
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, MaxBodySize)

	// Decode JSON directly from the limited reader (no intermediate buffer)
	decoder := json.NewDecoder(c.Request.Body)
	if err := decoder.Decode(dst); err != nil {
		if err == io.ErrUnexpectedEOF || err == io.EOF {
			return &ValidationError{Field: "body", Message: "empty or incomplete request body"}
		}
		if _, ok := err.(*json.SyntaxError); ok {
			return &ValidationError{Field: "body", Message: "invalid JSON syntax"}
		}
		if unmarshalErr, ok := err.(*json.UnmarshalTypeError); ok {
			return &ValidationError{Field: unmarshalErr.Field, Message: "wrong type for field"}
		}
		// Check for body-too-large (MaxBytesReader returns a specific error)
		if strings.Contains(err.Error(), "http: request body too large") {
			return &ValidationError{Field: "body", Message: "request body exceeds 1MB limit"}
		}
		return &ValidationError{Field: "body", Message: "invalid request format"}
	}

	// Run struct validation tags (binding:"required", etc.)
	if err := validate.Struct(dst); err != nil {
		if fieldErrs, ok := err.(validator.ValidationErrors); ok {
			if len(fieldErrs) > 0 {
				fe := fieldErrs[0]
				return &ValidationError{
					Field:   fe.Field(),
					Message: friendlyValidationMessage(fe),
				}
			}
		}
		return &ValidationError{Field: "body", Message: err.Error()}
	}

	return nil
}

// ValidationError represents a structured validation error.
type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func (e *ValidationError) Error() string {
	return e.Field + ": " + e.Message
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
func MaxBytesMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, MaxBodySize)
		c.Next()
	}
}
