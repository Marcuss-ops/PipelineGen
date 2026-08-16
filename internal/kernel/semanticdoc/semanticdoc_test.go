package semanticdoc

import (
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

func TestCompose_CanonicalEightFieldOrder(t *testing.T) {
	in := Input{
		AssetID:       "asset-1",
		Title:         "Round 7: Broner barcolla",
		Description:   "Broner loses composure on camera",
		VisualSummary: "a boxer ducks a punch and stumbles",
		Topics:        []string{"boxing", "round-7"},
		Entities:      []string{"Pacquiao", "Broner"},
		Event:         "Pacquiao vs Broner",
		Scene:         "main arena, Mandalay Bay",
	}
	doc := Compose(in)

	want := "Round 7: Broner barcolla\n" +
		"Broner loses composure on camera\n" +
		"a boxer ducks a punch and stumbles\n" +
		"topics: boxing, round-7\n" +
		"entities: Pacquiao, Broner\n" +
		"event: Pacquiao vs Broner\n" +
		"scene: main arena, Mandalay Bay"
	if doc.DocumentText != want {
		t.Fatalf("DocumentText mismatch.\n got = %q\nwant = %q", doc.DocumentText, want)
	}
	if doc.Version != "v3" {
		t.Fatalf("Version = %q, want v3", doc.Version)
	}
	if len(doc.Hash) != 64 {
		t.Fatalf("Hash length = %d, want 64 (sha256 hex)", len(doc.Hash))
	}
}

func TestCompose_DeterministicHash(t *testing.T) {
	in := Input{AssetID: "a", Title: "Jackie Chan Interview", Entities: []string{"Jackie Chan"}}
	a := Compose(in)
	b := Compose(in)
	if a.Hash != b.Hash || a.DocumentText != b.DocumentText {
		t.Fatalf("Compose is not deterministic: %+v vs %+v", a, b)
	}
	if a.Hash == "" {
		t.Fatal("Hash must not be empty")
	}
}

func TestCompose_HashChangesWithDocumentText(t *testing.T) {
	a := Compose(Input{AssetID: "a", Title: "One"})
	b := Compose(Input{AssetID: "a", Title: "Two"})
	if a.Hash == b.Hash {
		t.Fatal("changing the composed text must change the hash")
	}
}

func TestCompose_TranscriptMultilingual_OriginalFirstThenLangASC(t *testing.T) {
	in := Input{
		AssetID:          "asset-1",
		Title:            "Round 7 Test",
		OriginalLanguage: "en",
		Transcripts: []TranscriptSegment{
			{Lang: "es", Text: "Broner troppo lento en el ring.", IsOriginal: false},
			{Lang: "en", Text: "Pacquiao lands a clean left hook on Broner.", IsOriginal: true},
			{Lang: "it", Text: "Pacquiao molla Broner con un gancio.", IsOriginal: false},
		},
	}
	doc := Compose(in)

	originalIdx := strings.Index(doc.DocumentText, "Pacquiao lands a clean left hook on Broner.")
	esIdx := strings.Index(doc.DocumentText, "transcript (es):")
	itIdx := strings.Index(doc.DocumentText, "transcript (it):")
	if originalIdx < 0 || esIdx < 0 || itIdx < 0 {
		t.Fatalf("multilingual transcript missing from DocumentText: %q", doc.DocumentText)
	}
	if !(originalIdx < esIdx && originalIdx < itIdx) {
		t.Fatalf("original-language row must precede sequels: %q", doc.DocumentText)
	}
	if !(esIdx < itIdx) {
		t.Fatalf("sequels must be Lang-ASC (es before it): %q", doc.DocumentText)
	}
}

func TestCompose_TranscriptLegacySingleStringFallback(t *testing.T) {
	in := Input{AssetID: "a", Transcript: "legacy single-string transcript text"}
	doc := Compose(in)
	if !strings.Contains(doc.DocumentText, "legacy single-string transcript text") {
		t.Fatalf("legacy transcript fallback missing: %q", doc.DocumentText)
	}
}

func TestCompose_EvidencePassthrough(t *testing.T) {
	in := Input{
		AssetID: "asset-1",
		Title:   "Round 7: Broner barcolla",
		Evidence: asset.EvidenceDocument{
			AssetID:    "asset-1",
			Text:       "Pacquiao lands a clean left hook on Broner.",
			SourceType: asset.EvidenceTranscript,
			SourceHash: "deadbeef",
		},
	}
	doc := Compose(in)

	// The Evidence is carried through verbatim (separate field).
	if doc.Evidence.Text != in.Evidence.Text || doc.Evidence.SourceType != in.Evidence.SourceType {
		t.Fatalf("Evidence not carried through: %+v", doc.Evidence)
	}
	// The 8-field DocumentText must NOT be altered by the Evidence field.
	if !strings.Contains(doc.DocumentText, "Round 7: Broner barcolla") {
		t.Fatalf("DocumentText changed unexpectedly: %q", doc.DocumentText)
	}
	if strings.Contains(doc.DocumentText, "Pacquiao lands") {
		t.Fatalf("Evidence must not leak into the 8-field DocumentText: %q", doc.DocumentText)
	}
}

func TestCompose_EmptyInput_EmptyDocumentText(t *testing.T) {
	doc := Compose(Input{AssetID: "a"})
	if doc.DocumentText != "" {
		t.Fatalf("empty input must yield empty DocumentText, got %q", doc.DocumentText)
	}
	// The hash of the empty document text is still a well-defined SHA-256.
	if doc.Hash != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Fatalf("empty DocumentText hash = %q, want sha256('')", doc.Hash)
	}
}
