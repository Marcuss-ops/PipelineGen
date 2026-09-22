package workerruntime

import "testing"

func TestNewProfileRegistry_ContentIsDistinctFromCreator(t *testing.T) {
	reg := NewProfileRegistry()
	creator, err := reg.Lookup("creator")
	if err != nil {
		t.Fatal(err)
	}
	content, err := reg.Lookup("content")
	if err != nil {
		t.Fatal(err)
	}
	if content.MaxParallel != 2 {
		t.Fatalf("content MaxParallel = %d, want 2", content.MaxParallel)
	}
	if len(content.AllowedJobTypes) <= len(creator.AllowedJobTypes) {
		t.Fatalf("content profile should expose the broader full-composition ceiling: creator=%v content=%v", creator.AllowedJobTypes, content.AllowedJobTypes)
	}
	if !contains(content.AllowedJobTypes, "voiceover.generate") || !contains(content.AllowedJobTypes, "clip.render") {
		t.Fatalf("content profile missing core content capabilities: %v", content.AllowedJobTypes)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
