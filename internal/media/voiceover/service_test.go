package voiceover

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeBatchRequestDefaults(t *testing.T) {
	req := &BatchRequest{
		Text:      "Hello world",
		Languages: nil,
	}
	req = normalizeBatchRequest(req)

	assert.Equal(t, "{slug}_{lang}.mp3", req.FilenameTemplate, "default filename template")
	assert.Equal(t, "verify", req.Strategy, "default strategy")
	assert.Equal(t, []string{"en"}, req.Languages, "default language")
}

func TestNormalizeBatchRequestPreservesCustom(t *testing.T) {
	req := &BatchRequest{
		Text:             "Ciao mondo",
		Languages:        []string{"it"},
		FilenameTemplate: "{slug}_{lang}_{hash}.mp3",
		Strategy:         "replace",
	}
	req = normalizeBatchRequest(req)

	assert.Equal(t, "{slug}_{lang}_{hash}.mp3", req.FilenameTemplate)
	assert.Equal(t, "replace", req.Strategy)
	assert.Equal(t, []string{"it"}, req.Languages)
}

func TestTruncateString(t *testing.T) {
	tests := []struct {
		input    string
		maxLen   int
		expected string
	}{
		{"short", 100, "short"},
		{"hello world", 5, "hello"},
		{"", 10, ""},
		{"exactly", 7, "exactly"},
		{"too long text here", 3, "too"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := truncateString(tc.input, tc.maxLen)
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
			got := boolDefault(tc.input, tc.def)
			assert.Equal(t, tc.expected, got)
		})
	}
}

func TestBoolPtr(t *testing.T) {
	ptr := boolPtr(true)
	assert.NotNil(t, ptr)
	assert.True(t, *ptr)

	ptr = boolPtr(false)
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

func TestToSlug(t *testing.T) {
	tests := []struct {
		input  string
		maxLen int
		want   string
	}{
		{"Hello World", 50, "hello-world"},
		{"  Spaces  ", 50, "spaces"},
		{"UPPERCASE", 50, "uppercase"},
		{"special!@#chars", 50, "specialchars"},
		{"very long text that should be truncated", 10, "very-long-t"},
		{"", 50, ""},
		{"  ---trim---  ", 50, "trim"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := toSlug(tc.input, tc.maxLen)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		name        string
		outputDir   string
		filename    string
		wantErr     bool
		contains    string
	}{
		{"normal mp3", "/tmp/vo", "hello.mp3", false, "/tmp/vo/hello.mp3"},
		{"no extension adds mp3", "/tmp/vo", "hello", false, "/tmp/vo/hello.mp3"},
		{"path traversal blocked", "/tmp/vo", "../../etc/passwd", true, ""},
		{"nested path blocked", "/tmp/vo", "subdir/file.mp3", true, ""},
		{"empty output dir", "", "file.mp3", false, "file.mp3"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := sanitizeFilename(tc.outputDir, tc.filename)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Contains(t, result, tc.contains)
		})
	}
}

func TestTextToHash(t *testing.T) {
	h1 := textToHash("hello world")
	h2 := textToHash("hello world")
	h3 := textToHash("different text")

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

func TestBuildFilename(t *testing.T) {
	s := &Service{}
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
			req := &BatchRequest{
				Text:             tc.text,
				FilenameTemplate: tc.template,
			}
			filename := s.buildFilename(req, tc.lang, tc.hash)
			for _, check := range tc.checks {
				assert.Contains(t, filename, check)
			}
		})
	}
}

func TestBatchItemFail(t *testing.T) {
	item := BatchItem{
		ID:       "test-id",
		Language: "en",
		Status:   "processing",
	}

	result := item.fail("download_failed", assert.AnError)
	assert.Equal(t, "download_failed", result.Status)
	assert.Contains(t, result.Error, "assert.AnError")
	assert.Equal(t, "test-id", result.ID)
}

func TestBatchResponseConstruction(t *testing.T) {
	resp := &BatchResponse{
		OK:        true,
		RequestID: "vo_20250101_120000_abc123",
		Items: []BatchItem{
			{ID: "item-1", Language: "en", Status: "processed"},
			{ID: "item-2", Language: "it", Status: "processed"},
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
			{ID: "item-1", Language: "en", Status: "processed"},
			{ID: "item-2", Language: "it", Status: "failed", Error: "tts error"},
		},
	}

	assert.False(t, resp.OK)
	assert.Len(t, resp.Items, 2)
	assert.Equal(t, "failed", resp.Items[1].Status)
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
