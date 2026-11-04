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
