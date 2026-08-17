// Package indexing — payload_builder_tags_test.go
//
// Pins the Qdrant half of the METADATA THREADING BUG regression: the
// canonical writer-side payload builder must emit the request-provided
// Summary / Tags / Topics / MentionedPeople as their canonical payload
// keys (`summary`, `tags`, `topics`, `mentioned_people`). If any of the
// four is dropped here, the semantic-search surface silently loses the
// caller's metadata even though SQLite carried it. Stdlib `testing` only,
// matching the package-internal style of payload_builder_test.go.
package indexing

import (
	"reflect"
	"testing"
)

// TestBuildPayloadFromDocument_RequestProvidedSemanticKeys pins the
// request-provided semantic surface → Qdrant payload key mapping.
func TestBuildPayloadFromDocument_RequestProvidedSemanticKeys(t *testing.T) {
	doc := emptyDoc()
	doc.Metadata.Summary = "input summary"
	doc.Metadata.Tags = []string{"tag-a", "tag-b"}
	doc.Metadata.Topics = []string{"input-topic"}
	doc.Metadata.Speakers = []string{"input-speaker"}
	doc.Metadata.MentionedPeople = []string{"input-person"}

	p := BuildPayloadFromDocument(doc, nil)

	// summary
	if got, ok := p["summary"].(string); !ok || got != "input summary" {
		t.Fatalf(`payload["summary"] = %v (ok=%v), want "input summary"`, p["summary"], ok)
	}
	// tags (canonical request-provided tags key)
	wantTags := []string{"tag-a", "tag-b"}
	gotTags, ok := p["tags"].([]string)
	if !ok {
		t.Fatalf(`payload["tags"] is %T, want []string`, p["tags"])
	}
	if !reflect.DeepEqual(gotTags, wantTags) {
		t.Fatalf(`payload["tags"] = %v, want %v`, gotTags, wantTags)
	}
	// topics
	wantTopics := []string{"input-topic"}
	gotTopics, ok := p["topics"].([]string)
	if !ok || !reflect.DeepEqual(gotTopics, wantTopics) {
		t.Fatalf(`payload["topics"] = %v (ok=%v), want %v`, p["topics"], ok, wantTopics)
	}
	// mentioned_people
	wantMentioned := []string{"input-person"}
	gotMentioned, ok := p["mentioned_people"].([]string)
	if !ok || !reflect.DeepEqual(gotMentioned, wantMentioned) {
		t.Fatalf(`payload["mentioned_people"] = %v (ok=%v), want %v`, p["mentioned_people"], ok, wantMentioned)
	}
}

// TestBuildPayloadFromDocument_EmptySemanticKeysOmitted pins the omitempty
// counterpart: zero-value semantic fields must NOT emit payload keys (the
// godlike/07 no-fake-availability contract) — the regression must not
// reintroduce empty tags/topics/mentioned_people arrays.
func TestBuildPayloadFromDocument_EmptySemanticKeysOmitted(t *testing.T) {
	doc := emptyDoc() // Summary / Tags / Topics / Speakers / MentionedPeople all zero

	p := BuildPayloadFromDocument(doc, nil)

	for _, key := range []string{"summary", "tags", "topics", "mentioned_people"} {
		if _, present := p[key]; present {
			t.Errorf("payload[%q] must be ABSENT when the metadata field is empty (omitempty contract)", key)
		}
	}
}
