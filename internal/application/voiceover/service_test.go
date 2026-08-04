package voiceover

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	hashutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
	ptrutil "github.com/Marcuss-ops/PipelineGen/pkg/ptrutil"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func TestNormalizeBatchRequestDefaults(t *testing.T) {
	req := &BatchRequest{
		Text:      "Hello world",
		Languages: nil,
	}
	if req != nil {
		*req = normalizeBatchRequest(*req)
	}

	assert.Equal(t, "{slug}_{lang}.mp3", req.FilenameTemplate, "default filename template")
	assert.Equal(t, "verify", req.Strategy, "default strategy")
	assert.Equal(t, []Language{"en"}, req.Languages, "default language")
}

func TestNormalizeBatchRequestPreservesCustom(t *testing.T) {
	req := &BatchRequest{
		Text:             "Ciao mondo",
		Languages:        []Language{"it"},
		FilenameTemplate: "{slug}_{lang}_{hash}.mp3",
		Strategy:         "replace",
	}
	if req != nil {
		*req = normalizeBatchRequest(*req)
	}

	assert.Equal(t, "{slug}_{lang}_{hash}.mp3", req.FilenameTemplate)
	assert.Equal(t, "replace", req.Strategy)
	assert.Equal(t, []Language{"it"}, req.Languages)
}

func TestTruncateString(t *testing.T) {
	tests := []struct {
		input    string
		maxLen   int
		expected string
	}{
		{"short", 100, "short"},
		{"hello world", 5, "he..."},
		{"", 10, ""},
		{"exactly", 7, "exactly"},
		{"too long text here", 10, "too lon..."},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := textutil.Truncate(tc.input, tc.maxLen)
			assert.Equal(t, tc.expected, got)
		})
	}
}

func TestBoolDefault(t *testing.T) {
	trueVal := true
	falseVal := false

	tests := []struct {
		name     string
		input    *bool
		def      bool
		expected bool
	}{
		{"nil default true", nil, true, true},
		{"nil default false", nil, false, false},
		{"ptr true default false", &trueVal, false, true},
		{"ptr false default true", &falseVal, true, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ptrutil.BoolDefault(tc.input, tc.def)
			assert.Equal(t, tc.expected, got)
		})
	}
}

func TestBoolPtr(t *testing.T) {
	ptr := ptrutil.Bool(true)
	assert.NotNil(t, ptr)
	assert.True(t, *ptr)

	ptr = ptrutil.Bool(false)
	assert.NotNil(t, ptr)
	assert.False(t, *ptr)
}

func TestBuildRequestID(t *testing.T) {
	id1 := buildRequestID()
	id2 := buildRequestID()

	assert.Contains(t, id1, "vo_")
	assert.Len(t, id1, len("vo_20060102_150405_xxxxxx"))
	assert.NotEqual(t, id1, id2, "request IDs should be unique")
}

func TestRandomSuffixEdgeCases(t *testing.T) {
	empty := randomSuffix(0)
	assert.Empty(t, empty)

	negative := randomSuffix(-1)
	assert.Empty(t, negative)
}

func TestSlugifyWithMax(t *testing.T) {
	tests := []struct {
		input  string
		maxLen int
		want   string
	}{
		{"Hello World", 50, "hello-world"},
		{"  Spaces  ", 50, "spaces"},
		{"UPPERCASE", 50, "uppercase"},
		{"special!@#chars", 50, "special-chars"},
		{"very long text that should be truncated", 10, "very-long"},
		{"", 50, ""},
		{"  ---trim---  ", 50, "trim"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := textutil.SlugifyWithMax(tc.input, tc.maxLen)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestSanitizeFilename(t *testing.T) {
	outputDir := filepath.Join("tmp", "vo")
	tests := []struct {
		name      string
		outputDir string
		filename  string
		wantErr   bool
		contains  string
	}{
		{"normal mp3", outputDir, "hello.mp3", false, filepath.Join("tmp", "vo", "hello.mp3")},
		{"no extension adds mp3", outputDir, "hello", false, filepath.Join("tmp", "vo", "hello.mp3")},
		{"path traversal blocked", outputDir, "../../etc/passwd", true, ""},
		{"nested path blocked", outputDir, "subdir/file.mp3", true, ""},
		{"empty output dir", "", "file.mp3", false, "file.mp3"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := SanitizeFilename(tc.outputDir, tc.filename)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Contains(t, result, tc.contains)
		})
	}
}

func TestSHA256String(t *testing.T) {
	h1 := hashutil.SHA256String("hello world")
	h2 := hashutil.SHA256String("hello world")
	h3 := hashutil.SHA256String("different text")

	assert.Equal(t, h1, h2, "same input should produce same hash")
	assert.NotEqual(t, h1, h3, "different input should produce different hash")
	assert.Len(t, h1, 64, "SHA256 hex should be 64 chars")
}

func TestBuildVoiceoverID(t *testing.T) {
	id1 := buildVoiceoverID("hash123", "en", "folder-123")
	id2 := buildVoiceoverID("hash123", "en", "folder-123")
	id3 := buildVoiceoverID("hash456", "it", "folder-456")

	assert.Contains(t, id1, "vo_")
	assert.Equal(t, id1, id2, "same inputs should produce same ID")
	assert.NotEqual(t, id1, id3, "different inputs should produce different ID")
}

// TestBuildFilename keeps the canonical filename-construction
// coverage after E4 (June 2026) collapsed Service.buildFilename +
// buildCommandFilenameForItem + jobs.buildItemFilename into the
// single free function voiceover.BuildVoiceoverFilename(FilenameSpec).
// The receiver-less free function is the new canonical surface;
// tests that need Service state would belong in the filename_test.go
// family, but the {slug}/{lang}/{hash}/{time} token grammar is
// purely a function of FilenameSpec so no Service dependency is
// needed.
func TestBuildFilename(t *testing.T) {
	tests := []struct {
		name     string
		template string
		text     string
		lang     string
		hash     string
		checks   []string
	}{
		{
			name:     "default template",
			template: "",
			text:     "Hello World",
			lang:     "en",
			hash:     "abc123def456",
			checks:   []string{"hello-world", "_en", ".mp3"},
		},
		{
			name:     "custom with hash",
			template: "{hash}_{slug}_{lang}.wav",
			text:     "Test",
			lang:     "it",
			hash:     "deadbeef1234",
			checks:   []string{"deadbeef", "test", "_it", ".wav"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			filename, err := BuildVoiceoverFilename(FilenameSpec{
				Text:     tc.text,
				Language: Language(tc.lang),
				TextHash: tc.hash,
				Template: tc.template,
			})
			assert.NoError(t, err, "BuildVoiceoverFilename must accept the validated spec")
			for _, check := range tc.checks {
				assert.Contains(t, filename, check, "filename=%q must contain %q", filename, check)
			}
		})
	}
}

func TestBatchItemFail(t *testing.T) {
	item := BatchItem{
		ID:       "test-id",
		Language: Language("en"),
		Status:   StatusProcessing,
	}

	result := item.fail(FailureDownload, assert.AnError)
	assert.Equal(t, StatusFailed, result.Status, "PR-VO-AUDIT-P01: fail() normalises any failure code to StatusFailed")
	assert.Equal(t, []FailureCode{FailureDownload}, result.Errors, "PR-VO-AUDIT-P01: fail() appends the FailureCode to Errors[]")
	assert.Contains(t, result.Error, "assert.AnError")
	assert.Equal(t, "test-id", result.ID)
}

func TestBatchResponseConstruction(t *testing.T) {
	resp := &BatchResponse{
		OK:        true,
		RequestID: "vo_20250101_120000_abc123",
		Items: []BatchItem{
			{ID: "item-1", Language: Language("en"), Status: StatusCompleted},
			{ID: "item-2", Language: Language("it"), Status: StatusCompleted},
		},
	}

	assert.True(t, resp.OK)
	assert.Len(t, resp.Items, 2)
	assert.Equal(t, "item-1", resp.Items[0].ID)
}

func TestBatchResponseWithError(t *testing.T) {
	resp := &BatchResponse{
		OK:    false,
		Error: "some batch items failed",
		Items: []BatchItem{
			{ID: "item-1", Language: Language("en"), Status: StatusCompleted},
			{ID: "item-2", Language: Language("it"), Status: StatusFailed, Error: "tts error"},
		},
	}

	assert.False(t, resp.OK)
	assert.Len(t, resp.Items, 2)
	assert.Equal(t, StatusFailed, resp.Items[1].Status, "PR-VO-AUDIT-P01: failed item must surface typed StatusFailed")
	assert.Contains(t, resp.Items[1].Error, "tts")
}

func TestDestinationRequestFields(t *testing.T) {
	d := &DestinationRequest{
		Group:           "Test",
		FolderID:        "folder-123",
		FolderPath:      "/test/path",
		SubfolderName:   "sub",
		CreateSubfolder: true,
	}

	assert.Equal(t, "Test", d.Group)
	assert.Equal(t, "folder-123", d.FolderID)
	assert.True(t, d.CreateSubfolder)
}

func TestResolvedDestinationDefaults(t *testing.T) {
	d := &ResolvedDestination{}
	assert.Empty(t, d.Group)
	assert.Empty(t, d.FolderID)
	assert.Empty(t, d.FolderPath)
	assert.Empty(t, d.DriveLink)
}

// PR-VO-AUDIT-P02 (June 2026): the canonical destination resolver
// (destination_resolver.go) replaces the legacy
// `if req.Destination != nil` gate in stages.go::GenerateBatch.
// The new contract: every GenerateBatch call goes through the
// resolver, even when req.Destination is nil. This boundary test
// pins the new behaviour end-to-end: nil-Destination + empty
// (zero-value) cfg.Drive.VoiceoverFolder() =
// ErrMissingFolder. Pre-refactor, the same input silently fell
// through with `dest=nil` and surfaced at Stage 2 with
// `missing_folder_id` per-item. Post-refactor, the canonical
// resolver short-circuits at the resolve step.
func TestGenerateBatch_NilDestination_NoDefault_ReturnsMissingFolder(t *testing.T) {
	s := &Service{
		log: zap.NewNop(),
		// cfg intentionally nil: DoCompose or similar test fixtures
		// must not panic on a nil cfg receiver (the wrapper reads
		// s.cfg.Drive nil-safe).
		// assetDestResolver intentionally nil: Resolver is NOT consulted
		// for nil-dest + no-default (canonical resolver short-circuits).
	}
	req := &BatchRequest{
		Text:      "hello world",
		Languages: []Language{"en"},
		Strategy:  "replace",
		// Destination intentionally nil.
	}

	resp, err := s.GenerateBatch(context.Background(), req)
	assert.Error(t, err, "GenerateBatch must error on nil-dest when no default folder is configured")
	if err == nil {
		return
	}
	// The wrapped error must carry the canonical log-marker so it
	// survives the existing `missing_folder_id` telemetry surface.
	assert.Contains(t, err.Error(), "missing_folder_id",
		"error must surface missing_folder_id for fleet monitoring parity")
	if resp == nil {
		t.Fatal("GenerateBatch must return a structured failure response on resolve error")
	}
	assert.False(t, resp.OK)
	assert.Equal(t, VoiceoverDestinationUnavailableCode, resp.ErrorCode)
	assert.Contains(t, resp.Error, "destination resolve")
}

// PR-VO-AUDIT-P02 (June 2026): pinning the GenerateBatch
// fail-fast contract for the path-traversal attack vector set.
// Service{} zero-value is sufficient because the path-traversal
// rejection in DestinationRequest.Validate runs BEFORE any field
// access (audio processor, lifecycle service, db handle).
// A future refactor that moves the Validate call after service-field
// usage would break this test loudly — that's the audit pin.
func TestGenerateBatch_RejectsPathTraversalPayload(t *testing.T) {
	attacks := []string{
		"..",
		"../etc",
		"/etc/passwd",
		"subfolder/../sibling",
		".." + string(filepath.Separator) + "windows",
		strings.Repeat("a", 201), // length cap
	}
	for _, sub := range attacks {
		t.Run("attack-"+sub, func(t *testing.T) {
			s := &Service{
				// zap.NewNop() returns a logger that silently discards all
				// log entries — required because Service{}.log is nil and
				// s.log.Warn() panics on a nil receiver. We do NOT need a
				// live DB / audioProcessor / lifecycleService because the
				// Validate() guard fires BEFORE any field access.
				log: zap.NewNop(),
			}
			req := &BatchRequest{
				Text:      "hello world",
				Languages: []Language{"en"},
				Strategy:  "replace",
				Destination: &DestinationRequest{
					SubfolderName:   sub,
					CreateSubfolder: true,
				},
			}
			resp, err := s.GenerateBatch(context.Background(), req)
			if resp != nil {
				t.Errorf("attack %q: GenerateBatch must return nil response, got %#v", sub, resp)
			}
			assert.Error(t, err, "attack %q: GenerateBatch must reject", sub)
			if err == nil {
				return
			}
			// The exact error word varies (reserved/separator/traversal) but
			// every reject must mention subfolder_name OR a recognisable
			// substring. Defensive: tolerate pkg/pathutil wording drift by
			// allowing any of the canonical reject verbs.
			msg := err.Error()
			if !strings.Contains(msg, "subfolder_name") &&
				!strings.Contains(msg, "traversal") &&
				!strings.Contains(msg, "reserved") &&
				!strings.Contains(msg, "separator") {
				t.Errorf("attack %q: error %q must mention subfolder_name/traversal/reserved/separator", sub, msg)
			}
		})
	}
}
