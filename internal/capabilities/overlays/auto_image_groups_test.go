package overlays

import "testing"

func autoTestImage(id string, startMs, endMs int64) ImageCandidate {
	return ImageCandidate{
		AssetID:    id,
		URL:        "https://example.test/" + id + ".jpg",
		SHA256:     "sha-" + id,
		MediaType:  "image/jpeg",
		StartMs:    startMs,
		EndMs:      endMs,
		StartUS:    startMs * 1_000,
		DurationUS: (endMs - startMs) * 1_000,
	}
}

func TestAutoImageGroupsClustersNearbyCertifiedImages(t *testing.T) {
	images := []ImageCandidate{
		autoTestImage("a", 0, 900),
		autoTestImage("b", 1_000, 1_900),
		autoTestImage("c", 2_000, 2_900),
		autoTestImage("far", 60_000, 60_900),
	}
	groups, consumed := AutoImageGroups("scene-1", images, nil)
	if len(groups) != 1 {
		t.Fatalf("groups = %d, want 1", len(groups))
	}
	if groups[0].Count != 3 || groups[0].SceneID != "scene-1" {
		t.Fatalf("group count=%d scene=%q, want 3 in scene-1", groups[0].Count, groups[0].SceneID)
	}
	for _, id := range []string{"a", "b", "c"} {
		if _, ok := consumed[id]; !ok {
			t.Errorf("asset %q should be consumed by the group", id)
		}
	}
	if _, ok := consumed["far"]; ok {
		t.Error("distant singleton must not be consumed by a group")
	}
}

func TestAutoImageGroupsExcludesUncertifiedDuplicateAndOwnedAssets(t *testing.T) {
	noHash := autoTestImage("nohash", 0, 900)
	noHash.SHA256 = ""
	images := []ImageCandidate{
		autoTestImage("a", 0, 900),
		autoTestImage("a", 100, 1_000), // duplicate asset id
		noHash,
		autoTestImage("owned", 1_000, 1_900),
		autoTestImage("b", 1_200, 2_100),
	}
	exclude := map[string]struct{}{"owned": {}}
	groups, consumed := AutoImageGroups("scene-1", images, exclude)
	if len(groups) != 1 || groups[0].Count != 2 {
		t.Fatalf("only a and b are eligible (1200ms apart): want one group of 2, got %d groups", len(groups))
	}
	if len(consumed) != 2 {
		t.Fatalf("consumed = %v, want exactly a and b", consumed)
	}
	for _, id := range []string{"nohash", "owned"} {
		if _, ok := consumed[id]; ok {
			t.Errorf("%q must not be consumed", id)
		}
	}
}

func TestAutoImageGroupsCapsAtFiveImages(t *testing.T) {
	var images []ImageCandidate
	for i := 0; i < 7; i++ {
		start := int64(i) * 500
		images = append(images, autoTestImage(string(rune('a'+i)), start, start+400))
	}
	groups, _ := AutoImageGroups("scene-1", images, nil)
	for _, g := range groups {
		if g.Count > 5 {
			t.Fatalf("group %q has %d images, limit is 5", g.GroupID, g.Count)
		}
	}
}
