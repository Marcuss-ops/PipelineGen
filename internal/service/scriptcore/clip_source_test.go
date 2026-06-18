package scriptcore

import (
	"context"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/media/models"
	"github.com/Marcuss-ops/PipelineGen/internal/repository/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/database"
)

// clipSourceTestSchema composes the canonical media_assets CREATE TABLE
// from internal/storage/canonical.go::CanonicalMediaAssetsSchema. The
// canonical block covers all 39 columns clips.Repository.mediaAssetColumns
// ships today (and any column added by a future canonical migration
// without touching this file).
const clipSourceTestSchema = storage.CanonicalMediaAssetsSchema

// insertTestClip is a helper to insert a test clip into the DB.
func insertTestClip(t *testing.T, repo *clips.Repository, clip *models.MediaAsset) {
	t.Helper()
	ctx := context.Background()
	if err := repo.UpsertClip(ctx, clip); err != nil {
		t.Fatalf("failed to insert test clip %q: %v", clip.ID, err)
	}
}

// newClipSourceBuilder creates a ClipSourceBuilder backed by an in-memory SQLite DB.
func newClipSourceBuilder(t *testing.T) (*ClipSourceBuilder, *clips.Repository) {
	t.Helper()
	db := storage.NewTestDBWithSchema(t, clipSourceTestSchema)
	t.Cleanup(func() { db.Close() })
	repo := clips.NewRepository(db, zap.NewNop())
	builder := NewClipSourceBuilder(repo, nil, zap.NewNop())
	return builder, repo
}

// ── Test: BuildPack with valid clips ───────────────────────────────────

func TestBuildPack_AllValid(t *testing.T) {
	ctx := context.Background()
	builder, repo := newClipSourceBuilder(t)

	// Insert clips with metadata directly via the repo upsert path.
	// clean_transcript is stored in metadata_json — we set it via SetMetadataString.
	for _, clip := range []struct {
		id           string
		name         string
		duration     int
		transcript   string
		summary      string
		topics       string // JSON array
		qualityScore float64
		hook         string
		language     string
	}{
		{
			id:           "clip-1",
			name:         "Pompeii Eruption",
			duration:     480,
			transcript:   "Mount Vesuvius erupted in 79 AD. The city of Pompeii was buried under ash and pumice. Thousands of people perished. The city remained frozen in time for centuries.",
			summary:      "The eruption of Mount Vesuvius destroyed Pompeii in 79 AD, burying it under volcanic ash.",
			topics:       `["volcano","ancient rome","disaster"]`,
			qualityScore: 0.85,
			hook:         "What would it be like to witness a city frozen in time?",
			language:     "en",
		},
		{
			id:           "clip-2",
			name:         "Roman Architecture",
			duration:     600,
			transcript:   "Roman architects developed revolutionary techniques. They invented concrete and used arches extensively. The Colosseum could hold 50,000 spectators. Aqueducts brought water from miles away.",
			summary:      "Roman architectural innovations including concrete, arches, and large-scale infrastructure.",
			topics:       `["architecture","engineering","ancient rome"]`,
			qualityScore: 0.92,
			hook:         "The Romans built structures that still stand today.",
			language:     "en",
		},
		{
			id:           "clip-3",
			name:         "Roman Daily Life",
			duration:     300,
			transcript:   "Daily life in ancient Rome centered around the forum. Citizens would gather to discuss politics and trade. The baths were social hubs. Gladiator games entertained the masses.",
			summary:      "Daily routines and social structures in ancient Rome.",
			topics:       `["daily life","society","ancient rome"]`,
			qualityScore: 0.78,
			hook:         "Life in ancient Rome was surprisingly familiar.",
			language:     "en",
		},
	} {
		asset := &models.MediaAsset{
			ID:           clip.id,
			Name:         clip.name,
			Source:       "youtube",
			Duration:     clip.duration,
			QualityScore: clip.qualityScore,
			Tags:         []string{"documentary"},
			CreatedAt:    time.Now(),
			UpdatedAt:    time.Now(),
		}
		asset.SetMetadataString("clean_transcript", clip.transcript)
		asset.SetMetadataString("clip_summary", clip.summary)
		asset.SetMetadataString("topics", clip.topics)
		asset.SetMetadataString("hook", clip.hook)
		asset.SetMetadataString("language", clip.language)
		insertTestClip(t, repo, asset)
	}

	opts := &ClipGenerationOptions{
		Language:         "en",
		Tone:             "documentary",
		TranscriptPolicy: "auto",
	}

	pack, err := builder.BuildPack(ctx, []string{"clip-1", "clip-2", "clip-3"}, opts)
	if err != nil {
		t.Fatalf("BuildPack failed: %v", err)
	}

	// All 3 clips should be accepted
	if pack.Requested != 3 {
		t.Errorf("Requested = %d, want 3", pack.Requested)
	}
	if pack.Accepted != 3 {
		t.Errorf("Accepted = %d, want 3", pack.Accepted)
	}
	if len(pack.Clips) != 3 {
		t.Fatalf("len(Clips) = %d, want 3", len(pack.Clips))
	}
	if len(pack.ExcludedClips) != 0 {
		t.Errorf("len(ExcludedClips) = %d, want 0", len(pack.ExcludedClips))
	}

	// Verify clip-1 has all metadata properly extracted
	c1 := pack.Clips[0]
	if c1.ClipID != "clip-1" {
		t.Errorf("Clips[0].ClipID = %q, want %q", c1.ClipID, "clip-1")
	}
	if c1.Title != "Pompeii Eruption" {
		t.Errorf("Clips[0].Title = %q, want %q", c1.Title, "Pompeii Eruption")
	}
	if c1.Summary == "" {
		t.Error("Clips[0].Summary should not be empty")
	}
	if len(c1.Topics) == 0 {
		t.Error("Clips[0].Topics should not be empty")
	}
	if c1.Topics[0] != "volcano" {
		t.Errorf("Clips[0].Topics[0] = %q, want %q", c1.Topics[0], "volcano")
	}
	if c1.Hook == "" {
		t.Error("Clips[0].Hook should not be empty")
	}
	if c1.Language != "en" {
		t.Errorf("Clips[0].Language = %q, want %q", c1.Language, "en")
	}
	if c1.DurationSec != 480 {
		t.Errorf("Clips[0].DurationSec = %d, want 480", c1.DurationSec)
	}
	if c1.QualityScore != 0.85 {
		t.Errorf("Clips[0].QualityScore = %.2f, want 0.85", c1.QualityScore)
	}

	// Verify transcript word counts
	for _, c := range pack.Clips {
		if c.TranscriptWords <= 0 {
			t.Errorf("Clip %q has TranscriptWords = %d, expected > 0", c.ClipID, c.TranscriptWords)
		}
	}

	// Verify evidence chunks are generated from transcript
	if len(c1.EvidenceChunks) == 0 {
		t.Error("Clips[0].EvidenceChunks should not be empty")
	}
	for _, chunk := range c1.EvidenceChunks {
		if chunk.Text == "" {
			t.Error("evidence chunk has empty text")
		}
		if chunk.StartMS < 0 {
			t.Errorf("evidence chunk has negative StartMS: %d", chunk.StartMS)
		}
		if chunk.EndMS <= chunk.StartMS && chunk.EndMS > 0 {
			t.Errorf("evidence chunk EndMS (%d) should be > StartMS (%d)", chunk.EndMS, chunk.StartMS)
		}
	}
}

// ── Test: BuildPack with excluded clips ────────────────────────────────

func TestBuildPack_ExcludesMissingTranscript(t *testing.T) {
	ctx := context.Background()
	builder, repo := newClipSourceBuilder(t)

	// Clip with no transcript set at all
	insertTestClip(t, repo, &models.MediaAsset{
		ID:        "no-transcript",
		Name:      "No Transcript Clip",
		Source:    "youtube",
		Duration:  120,
		Tags:      []string{},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	})

	opts := &ClipGenerationOptions{
		Language: "en",
		Tone:     "documentary",
	}

	pack, err := builder.BuildPack(ctx, []string{"no-transcript"}, opts)
	if err != nil {
		t.Fatalf("BuildPack failed: %v", err)
	}

	if pack.Requested != 1 {
		t.Errorf("Requested = %d, want 1", pack.Requested)
	}
	if pack.Accepted != 0 {
		t.Errorf("Accepted = %d, want 0", pack.Accepted)
	}
	if len(pack.Clips) != 0 {
		t.Errorf("len(Clips) = %d, want 0", len(pack.Clips))
	}
	if len(pack.ExcludedClips) != 1 {
		t.Fatalf("len(ExcludedClips) = %d, want 1", len(pack.ExcludedClips))
	}

	exc := pack.ExcludedClips[0]
	if exc.ClipID != "no-transcript" {
		t.Errorf("ExcludedClips[0].ClipID = %q, want %q", exc.ClipID, "no-transcript")
	}
	if exc.ExcludeReason != "no_transcript" {
		t.Errorf("ExcludedClips[0].ExcludeReason = %q, want %q", exc.ExcludeReason, "no_transcript")
	}
}

func TestBuildPack_ExcludesQualityTooLow(t *testing.T) {
	ctx := context.Background()
	builder, repo := newClipSourceBuilder(t)

	asset := &models.MediaAsset{
		ID:           "low-quality",
		Name:         "Low Quality Clip",
		Source:       "youtube",
		Duration:     120,
		QualityScore: 0.3,
		Tags:         []string{},
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	asset.SetMetadataString("clean_transcript", "This is a transcript with enough words to pass the minimum threshold.")
	insertTestClip(t, repo, asset)

	opts := &ClipGenerationOptions{
		Language:        "en",
		Tone:            "documentary",
		MinQualityScore: 0.5,
	}

	pack, err := builder.BuildPack(ctx, []string{"low-quality"}, opts)
	if err != nil {
		t.Fatalf("BuildPack failed: %v", err)
	}

	if len(pack.Clips) != 0 {
		t.Errorf("len(Clips) = %d, want 0 (should be excluded)", len(pack.Clips))
	}
	if len(pack.ExcludedClips) != 1 {
		t.Fatalf("len(ExcludedClips) = %d, want 1", len(pack.ExcludedClips))
	}
	if exc := pack.ExcludedClips[0]; exc.ExcludeReason != "quality_too_low:0.30<0.50" {
		t.Errorf("ExcludeReason = %q, want %q", exc.ExcludeReason, "quality_too_low:0.30<0.50")
	}
}

func TestBuildPack_ExcludesTranscriptTooShort(t *testing.T) {
	ctx := context.Background()
	builder, repo := newClipSourceBuilder(t)

	asset := &models.MediaAsset{
		ID:           "short-transcript",
		Name:         "Short Transcript Clip",
		Source:       "youtube",
		Duration:     120,
		QualityScore: 0.9,
		Tags:         []string{},
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	// Very short transcript
	asset.SetMetadataString("clean_transcript", "Only three words here.")
	insertTestClip(t, repo, asset)

	opts := &ClipGenerationOptions{
		Language:           "en",
		Tone:               "documentary",
		MinTranscriptWords: 10,
	}

	pack, err := builder.BuildPack(ctx, []string{"short-transcript"}, opts)
	if err != nil {
		t.Fatalf("BuildPack failed: %v", err)
	}

	if len(pack.Clips) != 0 {
		t.Errorf("len(Clips) = %d, want 0", len(pack.Clips))
	}
	if len(pack.ExcludedClips) != 1 {
		t.Fatalf("len(ExcludedClips) = %d, want 1", len(pack.ExcludedClips))
	}
	if exc := pack.ExcludedClips[0]; exc.ExcludeReason != "transcript_too_short:4<10" {
		t.Errorf("ExcludeReason = %q, want %q", exc.ExcludeReason, "transcript_too_short:4<10")
	}
}

func TestBuildPack_ExcludesNotFoundClip(t *testing.T) {
	ctx := context.Background()
	builder, _ := newClipSourceBuilder(t) // no clips inserted

	opts := &ClipGenerationOptions{
		Language: "en",
		Tone:     "documentary",
	}

	pack, err := builder.BuildPack(ctx, []string{"nonexistent-clip"}, opts)
	if err != nil {
		t.Fatalf("BuildPack failed: %v", err)
	}

	if pack.Requested != 1 {
		t.Errorf("Requested = %d, want 1", pack.Requested)
	}
	if pack.Accepted != 0 {
		t.Errorf("Accepted = %d, want 0", pack.Accepted)
	}
	if len(pack.Clips) != 0 {
		t.Errorf("len(Clips) = %d, want 0", len(pack.Clips))
	}
	if len(pack.ExcludedClips) != 1 {
		t.Fatalf("len(ExcludedClips) = %d, want 1", len(pack.ExcludedClips))
	}
	if exc := pack.ExcludedClips[0]; exc.ExcludeReason != "not_found" {
		t.Errorf("ExcludeReason = %q, want %q", exc.ExcludeReason, "not_found")
	}
}

// ── Test: BuildPack with mixed valid/excluded clips ────────────────────

func TestBuildPack_MixedValidity(t *testing.T) {
	ctx := context.Background()
	builder, repo := newClipSourceBuilder(t)

	// 2 valid, 1 no-transcript, 1 nonexistent = 2 accepted, 2 excluded
	asset := &models.MediaAsset{
		ID:           "valid-1",
		Name:         "Valid Clip One",
		Source:       "youtube",
		Duration:     100,
		QualityScore: 0.8,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	asset.SetMetadataString("clean_transcript", "This is a valid transcript for the first clip. It has enough words.")
	insertTestClip(t, repo, asset)

	asset2 := &models.MediaAsset{
		ID:           "valid-2",
		Name:         "Valid Clip Two",
		Source:       "youtube",
		Duration:     200,
		QualityScore: 0.9,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	asset2.SetMetadataString("clean_transcript", "This is a valid transcript for the second clip. It also has enough words to pass.")
	insertTestClip(t, repo, asset2)

	// No transcript — will be excluded
	insertTestClip(t, repo, &models.MediaAsset{
		ID:        "no-transcript",
		Name:      "No Transcript",
		Source:    "youtube",
		Duration:  150,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	})

	// nonexistent — will be excluded

	opts := &ClipGenerationOptions{
		Language: "en",
		Tone:     "documentary",
	}

	pack, err := builder.BuildPack(ctx, []string{"valid-1", "valid-2", "no-transcript", "nonexistent"}, opts)
	if err != nil {
		t.Fatalf("BuildPack failed: %v", err)
	}

	if pack.Requested != 4 {
		t.Errorf("Requested = %d, want 4", pack.Requested)
	}
	if pack.Accepted != 2 {
		t.Errorf("Accepted = %d, want 2", pack.Accepted)
	}
	if len(pack.Clips) != 2 {
		t.Errorf("len(Clips) = %d, want 2", len(pack.Clips))
	}
	if len(pack.ExcludedClips) != 2 {
		t.Fatalf("len(ExcludedClips) = %d, want 2", len(pack.ExcludedClips))
	}

	// Verify excluded clips have correct reasons
	reasons := make(map[string]string)
	for _, ec := range pack.ExcludedClips {
		reasons[ec.ClipID] = ec.ExcludeReason
	}
	if reasons["no-transcript"] != "no_transcript" {
		t.Errorf("no-transcript reason = %q, want %q", reasons["no-transcript"], "no_transcript")
	}
	if reasons["nonexistent"] != "not_found" {
		t.Errorf("nonexistent reason = %q, want %q", reasons["nonexistent"], "not_found")
	}

	// Verify accepted clips are in correct order (as requested)
	if pack.Clips[0].ClipID != "valid-1" {
		t.Errorf("Clips[0].ClipID = %q, want %q", pack.Clips[0].ClipID, "valid-1")
	}
	if pack.Clips[1].ClipID != "valid-2" {
		t.Errorf("Clips[1].ClipID = %q, want %q", pack.Clips[1].ClipID, "valid-2")
	}
}

// ── Test: BuildPack with empty clip IDs ─────────────────────────────────

func TestBuildPack_EmptyClipIDs(t *testing.T) {
	ctx := context.Background()
	builder, _ := newClipSourceBuilder(t)

	opts := &ClipGenerationOptions{Language: "en", Tone: "documentary"}

	_, err := builder.BuildPack(ctx, []string{}, opts)
	if err == nil {
		t.Fatal("expected error for empty clip IDs, got nil")
	}
	if err.Error() != "at least one clip ID is required" {
		t.Errorf("error = %q, want %q", err.Error(), "at least one clip ID is required")
	}
}

// ── Test: Transcript fallback chain ─────────────────────────────────────

func TestBuildPack_TranscriptFallback(t *testing.T) {
	ctx := context.Background()
	builder, repo := newClipSourceBuilder(t)

	// clean_transcript empty → fall back to "transcript"
	asset := &models.MediaAsset{
		ID:        "fallback-clip",
		Name:      "Fallback Clip",
		Source:    "youtube",
		Duration:  60,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	asset.SetMetadataString("transcript", "This is the secondary transcript field used as fallback.")
	insertTestClip(t, repo, asset)

	opts := &ClipGenerationOptions{
		Language: "en",
		Tone:     "documentary",
	}

	pack, err := builder.BuildPack(ctx, []string{"fallback-clip"}, opts)
	if err != nil {
		t.Fatalf("BuildPack failed: %v", err)
	}

	if len(pack.Clips) != 1 {
		t.Fatalf("len(Clips) = %d, want 1 (transcript should be from fallback)", len(pack.Clips))
	}
	if pack.Clips[0].TranscriptWords <= 0 {
		t.Errorf("TranscriptWords = %d, expected > 0 from fallback transcript", pack.Clips[0].TranscriptWords)
	}
}

// ── Test: Speakers and mentioned people ─────────────────────────────────

func TestBuildPack_SpeakersAndPeople(t *testing.T) {
	ctx := context.Background()
	builder, repo := newClipSourceBuilder(t)

	asset := &models.MediaAsset{
		ID:        "people-clip",
		Name:      "Interview Clip",
		Source:    "youtube",
		Duration:  300,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	asset.SetMetadataString("clean_transcript", "This is an interview transcript with speakers mentioned.")
	asset.SetMetadataString("speakers", `["Dr. Smith","Prof. Jones"]`)
	asset.SetMetadataString("mentioned_people", `["Julius Caesar","Cleopatra"]`)
	insertTestClip(t, repo, asset)

	opts := &ClipGenerationOptions{
		Language: "en",
		Tone:     "documentary",
	}

	pack, err := builder.BuildPack(ctx, []string{"people-clip"}, opts)
	if err != nil {
		t.Fatalf("BuildPack failed: %v", err)
	}

	if len(pack.Clips) != 1 {
		t.Fatalf("len(Clips) = %d, want 1", len(pack.Clips))
	}

	c := pack.Clips[0]
	if len(c.Speakers) != 2 {
		t.Errorf("len(Speakers) = %d, want 2: %v", len(c.Speakers), c.Speakers)
	}
	if c.Speakers[0] != "Dr. Smith" {
		t.Errorf("Speakers[0] = %q, want %q", c.Speakers[0], "Dr. Smith")
	}
	if len(c.MentionedPeople) != 2 {
		t.Errorf("len(MentionedPeople) = %d, want 2: %v", len(c.MentionedPeople), c.MentionedPeople)
	}
	if c.MentionedPeople[1] != "Cleopatra" {
		t.Errorf("MentionedPeople[1] = %q, want %q", c.MentionedPeople[1], "Cleopatra")
	}
}

// ── Test: YouTubeTitle via metadata ─────────────────────────────────────

func TestBuildPack_YouTubeTitle(t *testing.T) {
	ctx := context.Background()
	builder, repo := newClipSourceBuilder(t)

	asset := &models.MediaAsset{
		ID:        "yt-clip",
		Name:      "Local Name",
		Source:    "youtube",
		Duration:  120,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	asset.SetMetadataString("clean_transcript", "Transcript for YouTube clip.")
	asset.SetMetadataString("youtube_title", "The Original YouTube Title")
	insertTestClip(t, repo, asset)

	opts := &ClipGenerationOptions{
		Language: "en",
		Tone:     "documentary",
	}

	pack, err := builder.BuildPack(ctx, []string{"yt-clip"}, opts)
	if err != nil {
		t.Fatalf("BuildPack failed: %v", err)
	}

	if len(pack.Clips) != 1 {
		t.Fatalf("len(Clips) = %d, want 1", len(pack.Clips))
	}
	if pack.Clips[0].YouTubeTitle != "The Original YouTube Title" {
		t.Errorf("YouTubeTitle = %q, want %q", pack.Clips[0].YouTubeTitle, "The Original YouTube Title")
	}
}

// ── Test: Evidence chunks generated from multi-paragraph transcript ─────

func TestBuildPack_EvidenceChunksFromMultiParagraph(t *testing.T) {
	ctx := context.Background()
	builder, repo := newClipSourceBuilder(t)

	asset := &models.MediaAsset{
		ID:        "multi-para",
		Name:      "Multi Paragraph Clip",
		Source:    "youtube",
		Duration:  180, // 3 minutes = 180s
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	asset.SetMetadataString("clean_transcript", "First paragraph about the introduction.\n\nSecond paragraph with more details.\n\nThird paragraph concluding the topic.")
	insertTestClip(t, repo, asset)

	opts := &ClipGenerationOptions{
		Language: "en",
		Tone:     "documentary",
	}

	pack, err := builder.BuildPack(ctx, []string{"multi-para"}, opts)
	if err != nil {
		t.Fatalf("BuildPack failed: %v", err)
	}

	if len(pack.Clips) != 1 {
		t.Fatalf("len(Clips) = %d, want 1", len(pack.Clips))
	}

	chunks := pack.Clips[0].EvidenceChunks
	if len(chunks) != 3 {
		t.Fatalf("len(EvidenceChunks) = %d, want 3 (one per paragraph)", len(chunks))
	}

	// Verify each chunk has correct text
	expectedTexts := []string{
		"First paragraph about the introduction.",
		"Second paragraph with more details.",
		"Third paragraph concluding the topic.",
	}
	for i, expected := range expectedTexts {
		if chunks[i].Text != expected {
			t.Errorf("EvidenceChunks[%d].Text = %q, want %q", i, chunks[i].Text, expected)
		}
	}

	// Verify timestamps: 180s total / 3 paragraphs = 60s each = 60000ms
	if chunks[0].StartMS != 0 {
		t.Errorf("EvidenceChunks[0].StartMS = %d, want 0", chunks[0].StartMS)
	}
	if chunks[0].EndMS != 60000 {
		t.Errorf("EvidenceChunks[0].EndMS = %d, want 60000", chunks[0].EndMS)
	}
	if chunks[1].StartMS != 60000 {
		t.Errorf("EvidenceChunks[1].StartMS = %d, want 60000", chunks[1].StartMS)
	}
	if chunks[2].StartMS != 120000 {
		t.Errorf("EvidenceChunks[2].StartMS = %d, want 120000", chunks[2].StartMS)
	}
	if chunks[2].EndMS != 180000 {
		t.Errorf("EvidenceChunks[2].EndMS = %d, want 180000", chunks[2].EndMS)
	}
}

// ── Test: Evidence chunks with zero duration ────────────────────────────

func TestBuildPack_EvidenceChunksWithoutDuration(t *testing.T) {
	ctx := context.Background()
	builder, repo := newClipSourceBuilder(t)

	// Clip with Duration = 0 — chunks should still be created but with zero timestamps
	asset := &models.MediaAsset{
		ID:        "no-duration",
		Name:      "No Duration Clip",
		Source:    "youtube",
		Duration:  0,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	asset.SetMetadataString("clean_transcript", "Paragraph one.\n\nParagraph two.")
	insertTestClip(t, repo, asset)

	opts := &ClipGenerationOptions{
		Language: "en",
		Tone:     "documentary",
	}

	pack, err := builder.BuildPack(ctx, []string{"no-duration"}, opts)
	if err != nil {
		t.Fatalf("BuildPack failed: %v", err)
	}

	if len(pack.Clips) != 1 {
		t.Fatalf("len(Clips) = %d, want 1", len(pack.Clips))
	}

	chunks := pack.Clips[0].EvidenceChunks
	if len(chunks) != 2 {
		t.Fatalf("len(EvidenceChunks) = %d, want 2", len(chunks))
	}

	// With Duration=0, chunkDuration = 0, so all timestamps should be 0
	for i, chunk := range chunks {
		if chunk.StartMS != 0 {
			t.Errorf("EvidenceChunks[%d].StartMS = %d, want 0 (no duration)", i, chunk.StartMS)
		}
		if chunk.EndMS != 0 {
			t.Errorf("EvidenceChunks[%d].EndMS = %d, want 0 (no duration)", i, chunk.EndMS)
		}
	}
}
