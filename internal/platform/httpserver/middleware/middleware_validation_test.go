package httpserver

import (
	"reflect"
	"testing"

	ut "github.com/go-playground/universal-translator"
)

func TestSanitizeString_TrimsWhitespace(t *testing.T) {
	got := SanitizeString("  hello world  ")
	if got != "hello world" {
		t.Fatalf("expected 'hello world', got %q", got)
	}
}

func TestSanitizeString_RemovesControlChars(t *testing.T) {
	got := SanitizeString("hello\x00world\x01test")
	if got != "helloworldtest" {
		t.Fatalf("expected 'helloworldtest', got %q", got)
	}
}

func TestSanitizeString_PreservesNewlines(t *testing.T) {
	got := SanitizeString("line1\nline2\r\nline3")
	if got != "line1\nline2\r\nline3" {
		t.Fatalf("expected preserved newlines, got %q", got)
	}
}

func TestSanitizeString_PreservesTabs(t *testing.T) {
	got := SanitizeString("col1\tcol2")
	if got != "col1\tcol2" {
		t.Fatalf("expected tab preserved, got %q", got)
	}
}

func TestSanitizeString_EmptyString(t *testing.T) {
	got := SanitizeString("")
	if got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestSanitizeString_OnlyWhitespace(t *testing.T) {
	got := SanitizeString("   ")
	if got != "" {
		t.Fatalf("expected empty after trim, got %q", got)
	}
}

func TestFriendlyValidationMessage_Required(t *testing.T) {
	msg := friendlyValidationMessage(mockFieldError{field: "Name", tag: "required"})
	if msg != "Name is required" {
		t.Fatalf("expected 'Name is required', got %q", msg)
	}
}

func TestFriendlyValidationMessage_URL(t *testing.T) {
	msg := friendlyValidationMessage(mockFieldError{field: "Link", tag: "url"})
	if msg != "Link must be a valid URL" {
		t.Fatalf("expected 'Link must be a valid URL', got %q", msg)
	}
}

func TestFriendlyValidationMessage_GTE(t *testing.T) {
	msg := friendlyValidationMessage(mockFieldError{field: "Count", tag: "gte", params: "1"})
	if msg != "Count must be at least 1" {
		t.Fatalf("expected 'Count must be at least 1', got %q", msg)
	}
}

func TestFriendlyValidationMessage_LTE(t *testing.T) {
	msg := friendlyValidationMessage(mockFieldError{field: "Count", tag: "lte", params: "100"})
	if msg != "Count must be at most 100" {
		t.Fatalf("expected 'Count must be at most 100', got %q", msg)
	}
}

func TestFriendlyValidationMessage_Min(t *testing.T) {
	msg := friendlyValidationMessage(mockFieldError{field: "Tags", tag: "min", params: "1"})
	if msg != "Tags must have at least 1 items" {
		t.Fatalf("expected 'Tags must have at least 1 items', got %q", msg)
	}
}

func TestFriendlyValidationMessage_Max(t *testing.T) {
	msg := friendlyValidationMessage(mockFieldError{field: "Tags", tag: "max", params: "10"})
	if msg != "Tags must have at most 10 items" {
		t.Fatalf("expected 'Tags must have at most 10 items', got %q", msg)
	}
}

func TestFriendlyValidationMessage_OneOf(t *testing.T) {
	msg := friendlyValidationMessage(mockFieldError{field: "Color", tag: "oneof", params: "red blue green"})
	if msg != "Color must be one of: red blue green" {
		t.Fatalf("expected 'Color must be one of: red blue green', got %q", msg)
	}
}

func TestFriendlyValidationMessage_UnknownTag(t *testing.T) {
	msg := friendlyValidationMessage(mockFieldError{field: "Field", tag: "custom"})
	if msg != "Field failed custom validation" {
		t.Fatalf("expected 'Field failed custom validation', got %q", msg)
	}
}

// P0 #2 (June 2026): the 4 TestIsPublicWebhookPath_* tests were
// removed along with the publicWebhookPaths bypass. The webhook path
// is now protected by the standard Auth middleware.

// mockFieldError creates a minimal validator.FieldError for testing.
type mockFieldError struct {
	field  string
	tag    string
	params string
}

func (m mockFieldError) Field() string                     { return m.field }
func (m mockFieldError) Tag() string                       { return m.tag }
func (m mockFieldError) Param() string                     { return m.params }
func (m mockFieldError) Error() string                     { return m.field + ": " + m.tag }
func (m mockFieldError) StructField() string               { return m.field }
func (m mockFieldError) Value() any                        { return nil }
func (m mockFieldError) ActualTag() string                 { return m.tag }
func (m mockFieldError) Namespace() string                 { return m.field }
func (m mockFieldError) StructNamespace() string           { return m.field }
func (m mockFieldError) Translate(ut ut.Translator) string { return m.Error() }
func (m mockFieldError) Kind() reflect.Kind                { return reflect.Invalid }
func (m mockFieldError) Type() reflect.Type                { return nil }
func (m mockFieldError) BadValue() any                     { return nil }
