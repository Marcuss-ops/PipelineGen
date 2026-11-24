package texttracks

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/translation"
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// The two materializers are deliberately distinct contracts: one produces
// text-track rows, the other produces ASS artifacts. These compile-time
// assertions make an accidental signature convergence fail the build.
var (
	_ func(context.Context, string, string, string, detail.TextTrackKind, []string) (*MaterializationReport, error) = (*Materializer)(nil).Materialize
	_ func(context.Context, SubtitleMaterializerInput) (*SubtitleMaterializerOutput, error)                         = (*SubtitleArtifactMaterializer)(nil).Materialize
)

func TestMaterializeConfigAppliesPerCallSourceAndOverride(t *testing.T) {
	base := ResolverConfig{SourceLanguage: "en", OverrideTargetLanguages: []string{"es"}}
	got := materializeConfig(base, "it", []string{"fr", "de"})

	if got.SourceLanguage != "it" {
		t.Fatalf("SourceLanguage = %q, want it", got.SourceLanguage)
	}
	if len(got.OverrideTargetLanguages) != 2 || got.OverrideTargetLanguages[0] != "fr" || got.OverrideTargetLanguages[1] != "de" {
		t.Fatalf("OverrideTargetLanguages = %v, want [fr de]", got.OverrideTargetLanguages)
	}
	if base.SourceLanguage != "en" || len(base.OverrideTargetLanguages) != 1 {
		t.Fatalf("materializeConfig mutated the base config: %+v", base)
	}
}

func TestValidateMaterializeRequest(t *testing.T) {
	cases := []struct {
		name    string
		assetID string
		source  string
		hash    string
		wantErr bool
	}{
		{name: "all empty", wantErr: true},
		{name: "missing source", assetID: "a", hash: "h", wantErr: true},
		{name: "missing hash", assetID: "a", source: "en", wantErr: true},
		{name: "valid", assetID: "a", source: "en", hash: "h"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateMaterializeRequest(tc.assetID, tc.source, tc.hash)
			if tc.wantErr {
				var invalid *ErrInvalidMaterializeRequest
				if !errors.As(err, &invalid) {
					t.Fatalf("error = %v, want ErrInvalidMaterializeRequest", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestTranslationFingerprintHonorsActualProvider(t *testing.T) {
	m := &Materializer{resolverCfg: ResolverConfig{
		TranslationModel: translation.ArgosTranslationModel,
		ModelVersion:     "argos-v1",
	}}

	model, version := m.translationFingerprint(translation.TranslationResult{
		UsedProvider: translation.ProviderOllama,
		UsedModel:    "llama3",
	})
	if model != translation.ProviderOllama || version != "llama3" {
		t.Fatalf("ollama fallback fingerprint = %q/%q, want ollama/llama3", model, version)
	}

	model, version = m.translationFingerprint(translation.TranslationResult{
		UsedProvider: translation.ProviderOllama,
	})
	if model != translation.ProviderOllama || version != "ollama-server-default" {
		t.Fatalf("default ollama fingerprint = %q/%q", model, version)
	}

	model, version = m.translationFingerprint(translation.TranslationResult{
		UsedProvider: "argos",
		UsedModel:    "argos-model",
	})
	if model != translation.ArgosTranslationModel || version != "argos-v1" {
		t.Fatalf("argos fingerprint = %q/%q, want argos-translate/argos-v1", model, version)
	}
}

func TestComputeSubtitleHashesIsDeterministic(t *testing.T) {
	in := SubtitleMaterializerInput{TimedCues: []detail.TimedCue{
		{StartMs: 0, EndMs: 100, Text: "hello"},
		{StartMs: 100, EndMs: 200, Text: "world"},
	}}
	textHash, cuesHashBytes, cuesHash, err := computeSubtitleHashes(in)
	if err != nil {
		t.Fatalf("computeSubtitleHashes: %v", err)
	}
	if len(textHash) != 64 || len(cuesHashBytes) != 64 {
		t.Fatalf("hash lengths = %d/%d, want 64/64", len(textHash), len(cuesHashBytes))
	}
	if cuesHash != cuesHashBytes[:8] {
		t.Fatalf("short cues hash = %q, want %q", cuesHash, cuesHashBytes[:8])
	}

	textHash2, cuesHashBytes2, _, err := computeSubtitleHashes(in)
	if err != nil {
		t.Fatalf("computeSubtitleHashes repeat: %v", err)
	}
	if textHash != textHash2 || cuesHashBytes != cuesHashBytes2 {
		t.Fatal("hashes are not deterministic")
	}

	in.TimedCues[1].Text = "world!"
	if _, cuesHashBytes3, _, err := computeSubtitleHashes(in); err != nil {
		t.Fatalf("computeSubtitleHashes changed: %v", err)
	} else if cuesHashBytes3 == cuesHashBytes2 {
		t.Fatal("cue hash did not change with cue text")
	}
}

func TestSubtitleArtifactMatches(t *testing.T) {
	in := SubtitleMaterializerInput{
		SubtitleStyleID: "style",
		ClipDurationMs:  1000,
		ClipContentHash: "clip",
	}
	current := &detail.SubtitleArtifact{
		Status:          detail.SubtitleStatusReady,
		IsCurrent:       true,
		TextHash:        "text",
		CuesHash:        "cues",
		StyleVersion:    "style",
		ClipDurationMs:  1000,
		ClipContentHash: "clip",
		DriveFileID:     "drive-id",
		DriveURL:        "https://drive.example/x",
	}

	if !subtitleArtifactMatches(current, in, "text", "cues") {
		t.Fatal("matching artifact was rejected")
	}
	if subtitleArtifactMatches(nil, in, "text", "cues") {
		t.Fatal("nil artifact matched")
	}
	current.Status = detail.SubtitleStatusFailed
	if subtitleArtifactMatches(current, in, "text", "cues") {
		t.Fatal("failed artifact matched")
	}
	current.Status = detail.SubtitleStatusReady
	current.DriveFileID = ""
	if subtitleArtifactMatches(current, in, "text", "cues") {
		t.Fatal("artifact without Drive id matched")
	}
	current.DriveFileID = "drive-id"
	current.CuesHash = "other"
	if subtitleArtifactMatches(current, in, "text", "cues") {
		t.Fatal("artifact with different cue hash matched")
	}
}

func TestSubtitlePublishFilename(t *testing.T) {
	cases := []struct {
		name string
		in   SubtitleMaterializerInput
		want string
	}{
		{name: "asset fallback", in: SubtitleMaterializerInput{AssetID: "asset", LanguageCode: "it"}, want: "asset.it.ass"},
		{name: "drive basename", in: SubtitleMaterializerInput{AssetID: "asset", LanguageCode: "it", DriveFilename: "clip.mp4"}, want: "clip.it.ass"},
		{name: "path basename", in: SubtitleMaterializerInput{AssetID: "asset", LanguageCode: "it", DriveFilename: "/tmp/clip"}, want: "clip.it.ass"},
		{name: "blank drive filename", in: SubtitleMaterializerInput{AssetID: "asset", LanguageCode: "it", DriveFilename: "   "}, want: "asset.it.ass"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := subtitlePublishFilename(tc.in); got != tc.want {
				t.Fatalf("subtitlePublishFilename = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSubtitleDriveSubpath(t *testing.T) {
	if got := subtitleDriveSubpath(SubtitleMaterializerInput{}); len(got) != 1 || got[0] != "Ass Sub" {
		t.Fatalf("nil subpath = %v, want [Ass Sub]", got)
	}
	if got := subtitleDriveSubpath(SubtitleMaterializerInput{DriveSubpath: []string{}}); len(got) != 0 {
		t.Fatalf("empty subpath = %v, want empty", got)
	}
	custom := []string{"Transcripts"}
	if got := subtitleDriveSubpath(SubtitleMaterializerInput{DriveSubpath: custom}); len(got) != 1 || got[0] != "Transcripts" {
		t.Fatalf("custom subpath = %v", got)
	}
}

func TestSubtitleDeliverySkipReason(t *testing.T) {
	s := &BackfillService{}
	if got := subtitleDeliverySkipReason(s, nil, detail.TextTrackTranscript); got != "no_asset" {
		t.Fatalf("nil asset reason = %q", got)
	}
	assetItem := &asset.Asset{ID: "asset-1", Source: asset.Source("youtube")}
	if got := subtitleDeliverySkipReason(s, assetItem, detail.TextTrackTranscript); got != "no_subtitle_materializer" {
		t.Fatalf("nil materializer reason = %q", got)
	}
	s.subMaterializer = &SubtitleArtifactMaterializer{}
	if got := subtitleDeliverySkipReason(s, &asset.Asset{ID: "asset-1", Source: asset.Source("unknown")}, detail.TextTrackTranscript); got != "source_does_not_take_subtitles" {
		t.Fatalf("source reason = %q", got)
	}
	if got := subtitleDeliverySkipReason(s, assetItem, detail.TextTrackDescription); got != "text_kind_is_not_transcript" {
		t.Fatalf("kind reason = %q", got)
	}
	if got := subtitleDeliverySkipReason(s, assetItem, detail.TextTrackTranscript); got != "" {
		t.Fatalf("valid request reason = %q, want empty", got)
	}
}

func TestBuildSubtitleOutputUsesLastCue(t *testing.T) {
	in := SubtitleMaterializerInput{TimedCues: []detail.TimedCue{
		{StartMs: 0, EndMs: 100, Text: "a"},
		{StartMs: 100, EndMs: 500, Text: "b"},
	}}
	out := buildSubtitleOutput(in, "/tmp/x.ass", "file-hash", "cues-hash", "text-hash", "")
	if out.CueCount != 2 || out.LastCueEndMs != 500 || out.CoveredDuration != 500 {
		t.Fatalf("output = %+v, want 2 cues ending at 500", out)
	}
	if out.LocalPath != "/tmp/x.ass" || out.ContentSHA256 != "file-hash" || out.CuesHash != "cues-hash" || out.TextHash != "text-hash" {
		t.Fatalf("output hashes/path not propagated: %+v", out)
	}
}

func TestBuildSubtitleArtifact(t *testing.T) {
	in := SubtitleMaterializerInput{
		AssetID:         "asset-1",
		LanguageCode:    "it",
		TextTrackID:     7,
		ClipDurationMs:  1000,
		ClipContentHash: "clip",
		SubtitleStyleID: "style",
	}
	out := &SubtitleMaterializerOutput{
		LocalPath:    "/tmp/x.ass",
		TextHash:     "text-hash",
		CuesHash:     "cues-hash",
		LastCueEndMs: 900,
	}
	art := buildSubtitleArtifact(in, out, "file-hash", detail.SubtitleStatusReady, "")
	if art.Format != detail.SubtitleFormatASS || art.Status != detail.SubtitleStatusReady || !art.IsCurrent {
		t.Fatalf("artifact format/status = %+v", art)
	}
	if art.GeneratorVersion != "vidrush-ass-v2" || art.CuesHash != "cues-hash" || art.ContentSHA256 != "file-hash" {
		t.Fatalf("artifact provenance = %+v", art)
	}
}

func TestFinalizeSubtitleDeliveryReportClearsEmptyMaps(t *testing.T) {
	rep := SubtitleDeliveryReport{Failed: map[string]string{}, UnTimed: map[string]string{}}
	finalizeSubtitleDeliveryReport(&rep)
	if rep.Failed != nil || rep.UnTimed != nil {
		t.Fatalf("empty maps were not cleared: %+v", rep)
	}
	rep = SubtitleDeliveryReport{Failed: map[string]string{"it": "boom"}, UnTimed: map[string]string{"fr": "no cues"}}
	finalizeSubtitleDeliveryReport(&rep)
	if len(rep.Failed) != 1 || len(rep.UnTimed) != 1 {
		t.Fatalf("non-empty maps were cleared: %+v", rep)
	}
}
