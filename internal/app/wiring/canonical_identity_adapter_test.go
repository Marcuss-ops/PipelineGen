package wiring

import (
	"context"
	"database/sql"
	"testing"
)

// TestNewCanonicalIdentityResolver_PrefersPostgres pins MEDIA-SSOT P1-8: when
// the media PostgreSQL SSOT is available, provider discovery canonicalises
// identity there instead of against the operational SQLite registry.
func TestNewCanonicalIdentityResolver_PrefersPostgres(t *testing.T) {
	resolver := newCanonicalIdentityResolver(&sql.DB{}, nil)
	adapter, ok := resolver.(*canonicalIdentityAdapter)
	if !ok {
		t.Fatalf("resolver = %T, want *canonicalIdentityAdapter", resolver)
	}
	if adapter.inner == nil {
		t.Fatal("adapter.inner must be wired")
	}
}

// TestNewCanonicalIdentityResolver_LegacyFallback pins the documented
// graceful-degrade path: media PostgreSQL intentionally disabled falls back to
// the legacy SQLite registry instead of failing boot.
func TestNewCanonicalIdentityResolver_LegacyFallback(t *testing.T) {
	resolver := newCanonicalIdentityResolver(nil, &sql.DB{})
	if _, ok := resolver.(*canonicalIdentityAdapter); !ok {
		t.Fatalf("resolver = %T, want *canonicalIdentityAdapter (legacy SQLite registry)", resolver)
	}
}

// TestNewCanonicalIdentityResolver_NoSurfaceIsFailClosed pins fail-closed
// wiring: no durable handle at all yields a resolver that never fabricates an
// identity.
func TestNewCanonicalIdentityResolver_NoSurfaceIsFailClosed(t *testing.T) {
	resolver := newCanonicalIdentityResolver(nil, nil)
	if _, ok := resolver.(*canonicalIdentityAdapter); ok {
		t.Fatal("no durable handle must not produce an adapter-backed resolver")
	}
	identity, err := resolver.ResolveSource(context.Background(), "youtube", "abc")
	if err != nil {
		t.Fatalf("noop resolver must not error, got %v", err)
	}
	if identity.Resolved {
		t.Fatal("noop resolver must never resolve an identity")
	}
}
