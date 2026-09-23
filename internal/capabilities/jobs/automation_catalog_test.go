package jobs

import "testing"

func TestAutomationCatalog_OnlyAdvertisesExternalSafeLiveJobs(t *testing.T) {
	catalog := NewAutomationCatalog(Compose(), []string{
		TypeScriptGenerate,
		TypeMediaStock,
		TypeSystemCleanup,
		TypeScriptGenerateItem,
	})

	if !catalog.Allows(TypeScriptGenerate) {
		t.Fatal("script.generate should be external-safe when live")
	}
	if !catalog.Allows(TypeMediaStock) {
		t.Fatal("media.stock should be external-safe when live")
	}
	if catalog.Allows(TypeSystemCleanup) {
		t.Fatal("system.cleanup must never be external-safe")
	}
	if catalog.Allows(TypeScriptGenerateItem) {
		t.Fatal("script.generate_item must remain an internal child job")
	}

	items := catalog.List()
	if len(items) != 2 {
		t.Fatalf("catalog length = %d, want 2 (%v)", len(items), items)
	}
	if items[0].Type != TypeMediaStock || items[1].Type != TypeScriptGenerate {
		t.Fatalf("catalog order = %q, %q; want stable lexical order", items[0].Type, items[1].Type)
	}
}

func TestAutomationCatalog_DoesNotAdvertiseUnimplementedWorkflowJobs(t *testing.T) {
	catalog := NewAutomationCatalog(Compose(), []string{TypeScriptGenerate})
	if catalog.Allows("video.create") || catalog.Allows("video.assemble") {
		t.Fatal("unimplemented workflow jobs must not be advertised")
	}
}

// TestAutomationCatalog_AdvertisesVideoCreateWhenLive pins the Sept 2026
// enablement: now that the durable video.create handler is registered
// (internal/capabilities/videocreate), the type is external-safe and
// M2M-submittable exactly when it is a live handler — and still hidden when
// the handler is absent (the fake-availability guard of the test above).
func TestAutomationCatalog_AdvertisesVideoCreateWhenLive(t *testing.T) {
	catalog := NewAutomationCatalog(Compose(), []string{TypeVideoCreate})
	if !catalog.Allows(TypeVideoCreate) {
		t.Fatal("video.create should be external-safe and M2M-submittable when its durable handler is live")
	}
	items := catalog.List()
	if len(items) != 1 || items[0].Type != TypeVideoCreate {
		t.Fatalf("catalog = %+v, want exactly [video.create]", items)
	}
	if items[0].RequiredScope != ScopeJobsSubmit {
		t.Fatalf("required_scope = %q, want %q", items[0].RequiredScope, ScopeJobsSubmit)
	}
}
