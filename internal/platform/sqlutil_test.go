package platform

import (
	"testing"
)

func TestBuildFallbackLikeConditions_EmptyTokens(t *testing.T) {
	query, args := BuildFallbackLikeConditions(nil, []string{"name"})
	if query != "" || args != nil {
		t.Errorf("expected empty for nil tokens, got query=%q args=%v", query, args)
	}
	query, args = BuildFallbackLikeConditions([]string{}, []string{"name"})
	if query != "" || args != nil {
		t.Errorf("expected empty for empty tokens, got query=%q args=%v", query, args)
	}
}

func TestBuildFallbackLikeConditions_EmptyColumns(t *testing.T) {
	query, args := BuildFallbackLikeConditions([]string{"hello"}, nil)
	if query != "" || args != nil {
		t.Errorf("expected empty for nil columns, got query=%q args=%v", query, args)
	}
}

func TestBuildFallbackLikeConditions_ShortTokens(t *testing.T) {
	query, args := BuildFallbackLikeConditions([]string{"a", "b"}, []string{"name"})
	if query != "" || args != nil {
		t.Errorf("expected empty for short tokens (< 2 chars), got query=%q args=%v", query, args)
	}
}

func TestBuildFallbackLikeConditions_SingleToken(t *testing.T) {
	query, args := BuildFallbackLikeConditions([]string{"hello"}, []string{"name"})
	expected := "((name LIKE ?))"
	if query != expected {
		t.Errorf("expected %q, got %q", expected, query)
	}
	if len(args) != 1 || args[0] != "%hello%" {
		t.Errorf("expected args [%s], got %v", "%hello%", args)
	}
}

func TestBuildFallbackLikeConditions_MultiTokenSingleColumn(t *testing.T) {
	query, args := BuildFallbackLikeConditions([]string{"hello", "world"}, []string{"name"})
	expected := "((name LIKE ?) AND (name LIKE ?))"
	if query != expected {
		t.Errorf("expected %q, got %q", expected, query)
	}
	if len(args) != 2 || args[0] != "%hello%" || args[1] != "%world%" {
		t.Errorf("unexpected args: %v", args)
	}
}

func TestBuildFallbackLikeConditions_MultiTokenMultiColumn(t *testing.T) {
	query, args := BuildFallbackLikeConditions([]string{"hello", "world"}, []string{"name", "description"})
	expected := "((name LIKE ? OR description LIKE ?) AND (name LIKE ? OR description LIKE ?))"
	if query != expected {
		t.Errorf("expected %q, got %q", expected, query)
	}
	if len(args) != 4 {
		t.Errorf("expected 4 args, got %d: %v", len(args), args)
	}
}

func TestBuildFallbackLikeConditions_TokenTrimming(t *testing.T) {
	query, args := BuildFallbackLikeConditions([]string{"  hello  ", "world"}, []string{"name"})
	expected := "((name LIKE ?) AND (name LIKE ?))"
	if query != expected {
		t.Errorf("expected %q, got %q", expected, query)
	}
	if args[0] != "%hello%" || args[1] != "%world%" {
		t.Errorf("expected trimmed tokens, got %v", args)
	}
}

func TestBuildFallbackLikeConditions_MixedLengthTokens(t *testing.T) {
	query, args := BuildFallbackLikeConditions([]string{"a", "hello", "b", "world"}, []string{"name"})
	expected := "((name LIKE ?) AND (name LIKE ?))"
	if query != expected {
		t.Errorf("expected %q, got %q", expected, query)
	}
	if len(args) != 2 || args[0] != "%hello%" || args[1] != "%world%" {
		t.Errorf("expected only long tokens, got %v", args)
	}
}
