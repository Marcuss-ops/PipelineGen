package scripts

import (
	"strings"
	"testing"
)

// ── Version constants ─────────────────────────────────────────────────────

func TestVersionConstants_NonEmpty(t *testing.T) {
	consts := map[string]string{
		"PlannerPromptVersion":     PlannerPromptVersion,
		"WriterPromptVersion":      WriterPromptVersion,
		"NormalizerVersion":        NormalizerVersion,
		"SceneBuilderVersion":      SceneBuilderVersion,
		"OutputSchemaVersion":      OutputSchemaVersion,
		"NarrativeStrategyVersion": NarrativeStrategyVersion,
	}
	for name, v := range consts {
		if v == "" {
			t.Errorf("%s is empty", name)
		}
		if !strings.HasPrefix(v, "v") {
			t.Errorf("%s = %q, want prefix 'v'", name, v)
		}
	}
}

func TestPlannerGenerationParams(t *testing.T) {
	// These constants are mixed into the fingerprint; if they change the
	// cache invalidation logic depends on the values being exposed.
	if PlannerTemperature <= 0 || PlannerTemperature > 1 {
		t.Errorf("PlannerTemperature = %v, want (0, 1]", PlannerTemperature)
	}
	if PlannerNumPredict <= 0 {
		t.Errorf("PlannerNumPredict = %d, want > 0", PlannerNumPredict)
	}
}

// ── FingerprintVersionContext helpers ─────────────────────────────────────

func TestFingerprintContext_DefaultIsComplete(t *testing.T) {
	ctx := DefaultFingerprintContext()
	if ctx.PlannerPromptVersion == "" || ctx.PlannerPromptVersion == UnknownVersion {
		t.Errorf("DefaultFingerprintContext should set PlannerPromptVersion, got %q", ctx.PlannerPromptVersion)
	}
	if ctx.PlannerModel != "" {
		// DefaultFingerprintContext leaves models empty intentionally
		// (the caller may not know the model name at fingerprint time).
		t.Errorf("DefaultFingerprintContext.PlannerModel = %q, want empty", ctx.PlannerModel)
	}
}

func TestFingerprintContext_NewFingerprintContextSetsModels(t *testing.T) {
	ctx := NewFingerprintContext("planner-model", "writer-model")
	if ctx.PlannerModel != "planner-model" {
		t.Errorf("PlannerModel = %q, want %q", ctx.PlannerModel, "planner-model")
	}
	if ctx.WriterModel != "writer-model" {
		t.Errorf("WriterModel = %q, want %q", ctx.WriterModel, "writer-model")
	}
}

func TestFingerprintContext_FillMissingCopyDoesNotMutate(t *testing.T) {
	// fillMissingCopy must NOT mutate the receiver. If a caller reuses
	// the same context across multiple fingerprint calls, the second
	// call should see the same empty fields, not the filled ones.
	original := &FingerprintVersionContext{
		PlannerPromptVersion: "v1",
		WriterModel:          "custom-model",
	}
	_ = original.fillMissingCopy()
	if original.PlannerPromptVersion != "v1" {
		t.Errorf("fillMissingCopy mutated PlannerPromptVersion: %q", original.PlannerPromptVersion)
	}
	if original.WriterModel != "custom-model" {
		t.Errorf("fillMissingCopy mutated WriterModel: %q", original.WriterModel)
	}
}

func TestFingerprintContext_FillMissingCopyOnNil(t *testing.T) {
	// nil receiver should not panic and should return the same hash input
	// as DefaultFingerprintContext() (real versions + v_unknown models).
	var c *FingerprintVersionContext
	out := c.fillMissingCopy()
	if out.PlannerPromptVersion != PlannerPromptVersion {
		t.Errorf("nil fillMissingCopy PlannerPromptVersion = %q, want %q", out.PlannerPromptVersion, PlannerPromptVersion)
	}
	if out.PlannerModel != UnknownVersion {
		t.Errorf("nil fillMissingCopy PlannerModel = %q, want %q (model fields stay v_unknown)", out.PlannerModel, UnknownVersion)
	}
}

func TestFingerprintContext_FillMissingCopyReplacesEmpty(t *testing.T) {
	c := &FingerprintVersionContext{}
	out := c.fillMissingCopy()
	if out.PlannerPromptVersion != UnknownVersion {
		t.Errorf("PlannerPromptVersion = %q, want %q", out.PlannerPromptVersion, UnknownVersion)
	}
	if out.WriterModel != UnknownVersion {
		t.Errorf("WriterModel = %q, want %q", out.WriterModel, UnknownVersion)
	}
	if out.TypeRegistryVersion != UnknownVersion {
		t.Errorf("TypeRegistryVersion = %q, want %q", out.TypeRegistryVersion, UnknownVersion)
	}
}

// ── ComputeFingerprint invalidation ──────────────────────────────────────

// makeFingerprintPack returns a minimal pack used by the fingerprint tests.
func makeFingerprintPack() *ClipSourcePack {
	return &ClipSourcePack{
		Clips: []ClipEvidence{
			{ClipID: "clip-a", Title: "Title A", Summary: "Summary A", TranscriptWords: 100},
			{ClipID: "clip-b", Title: "Title B", Summary: "Summary B", TranscriptWords: 150},
		},
	}
}

func makeFingerprintBuilder() *ClipSourceBuilder {
	return &ClipSourceBuilder{}
}

func TestComputeFingerprint_DeterministicForSameInputs(t *testing.T) {
	b := makeFingerprintBuilder()
	pack := makeFingerprintPack()
	opts := &ClipGenerationOptions{
		Language: "en",
		Tone:     "comedy",
		Type:     "compilation",
	}
	ctx := NewFingerprintContext("gemma2:9b", "gemma2:9b")

	fp1 := b.ComputeFingerprint([]string{"clip-a", "clip-b"}, pack, opts, ctx)
	fp2 := b.ComputeFingerprint([]string{"clip-a", "clip-b"}, pack, opts, ctx)
	if fp1 != fp2 {
		t.Errorf("fingerprint not deterministic: %q vs %q", fp1, fp2)
	}
	if !strings.HasPrefix(fp1, "cs_") {
		t.Errorf("fingerprint %q missing 'cs_' prefix", fp1)
	}
}

func TestComputeFingerprint_NilVersionContextUsesDefaults(t *testing.T) {
	b := makeFingerprintBuilder()
	pack := makeFingerprintPack()
	opts := &ClipGenerationOptions{Language: "en", Tone: "comedy"}

	fpNil := b.ComputeFingerprint([]string{"clip-a"}, pack, opts, nil)
	fpDefault := b.ComputeFingerprint([]string{"clip-a"}, pack, opts, DefaultFingerprintContext())
	if fpNil != fpDefault {
		t.Errorf("nil versionCtx should behave like DefaultFingerprintContext:\n  nil      = %q\n  default  = %q", fpNil, fpDefault)
	}
}

func TestComputeFingerprint_ChangesOnAllVersionBumps(t *testing.T) {
	b := makeFingerprintBuilder()
	pack := makeFingerprintPack()
	opts := &ClipGenerationOptions{Language: "en", Tone: "comedy"}

	fields := []string{
		"PlannerPromptVersion",
		"WriterPromptVersion",
		"NormalizerVersion",
		"SceneBuilderVersion",
		"TypeRegistryVersion",
		"OutputSchemaVersion",
	}

	for _, field := range fields {
		base := DefaultFingerprintContext()
		bumped := DefaultFingerprintContext()
		switch field {
		case "PlannerPromptVersion":
			bumped.PlannerPromptVersion = "v999"
		case "WriterPromptVersion":
			bumped.WriterPromptVersion = "v999"
		case "NormalizerVersion":
			bumped.NormalizerVersion = "v999"
		case "SceneBuilderVersion":
			bumped.SceneBuilderVersion = "v999"
		case "TypeRegistryVersion":
			bumped.TypeRegistryVersion = "v999"
		case "OutputSchemaVersion":
			bumped.OutputSchemaVersion = "v999"
		}

		fpBase := b.ComputeFingerprint([]string{"clip-a"}, pack, opts, base)
		fpBumped := b.ComputeFingerprint([]string{"clip-a"}, pack, opts, bumped)
		if fpBase == fpBumped {
			t.Errorf("%s bump did not change fingerprint: %q", field, fpBase)
		}
	}
}

func TestComputeFingerprint_ChangesOnModelChange(t *testing.T) {
	b := makeFingerprintBuilder()
	pack := makeFingerprintPack()
	opts := &ClipGenerationOptions{Language: "en", Tone: "comedy"}

	gemma := NewFingerprintContext("gemma2:9b", "gemma2:9b")
	llama := NewFingerprintContext("llama3:8b", "llama3:8b")

	fpGemma := b.ComputeFingerprint([]string{"clip-a"}, pack, opts, gemma)
	fpLlama := b.ComputeFingerprint([]string{"clip-a"}, pack, opts, llama)
	if fpGemma == fpLlama {
		t.Errorf("expected different fingerprints when model changes: %q", fpGemma)
	}
}

func TestComputeFingerprint_ChangesWhenOnlyPlannerModelChanges(t *testing.T) {
	b := makeFingerprintBuilder()
	pack := makeFingerprintPack()
	opts := &ClipGenerationOptions{Language: "en", Tone: "comedy"}

	same := NewFingerprintContext("gemma2:9b", "gemma2:9b")
	diff := NewFingerprintContext("llama3:8b", "gemma2:9b")

	fpSame := b.ComputeFingerprint([]string{"clip-a"}, pack, opts, same)
	fpDiff := b.ComputeFingerprint([]string{"clip-a"}, pack, opts, diff)
	if fpSame == fpDiff {
		t.Errorf("fingerprint should change when only planner model changes: %q", fpSame)
	}
}

func TestComputeFingerprint_ChangesWhenOnlyWriterModelChanges(t *testing.T) {
	b := makeFingerprintBuilder()
	pack := makeFingerprintPack()
	opts := &ClipGenerationOptions{Language: "en", Tone: "comedy"}

	same := NewFingerprintContext("gemma2:9b", "gemma2:9b")
	diff := NewFingerprintContext("gemma2:9b", "llama3:8b")

	fpSame := b.ComputeFingerprint([]string{"clip-a"}, pack, opts, same)
	fpDiff := b.ComputeFingerprint([]string{"clip-a"}, pack, opts, diff)
	if fpSame == fpDiff {
		t.Errorf("fingerprint should change when only writer model changes: %q", fpSame)
	}
}

func TestComputeFingerprint_VUnknownSentinelChangesHash(t *testing.T) {
	// Lock down that the v_unknown sentinel propagates into the actual
	// hash: an empty context (everything becomes v_unknown) must produce
	// a fingerprint different from DefaultFingerprintContext (which has
	// real version values but no models).
	b := makeFingerprintBuilder()
	pack := makeFingerprintPack()
	opts := &ClipGenerationOptions{Language: "en", Tone: "comedy"}

	empty := &FingerprintVersionContext{}
	defaultCtx := DefaultFingerprintContext()

	fpEmpty := b.ComputeFingerprint([]string{"clip-a"}, pack, opts, empty)
	fpDefault := b.ComputeFingerprint([]string{"clip-a"}, pack, opts, defaultCtx)
	if fpEmpty == fpDefault {
		t.Errorf("v_unknown propagation broken: empty ctx should differ from default ctx\n  empty   = %q\n  default = %q", fpEmpty, fpDefault)
	}
}

func TestComputeFingerprint_InsensitiveToClipIDOrder(t *testing.T) {
	// Existing behavior: clip IDs are sorted before hashing, so reordering
	// the same set of clip IDs should not change the fingerprint.
	b := makeFingerprintBuilder()
	pack := makeFingerprintPack()
	opts := &ClipGenerationOptions{Language: "en", Tone: "comedy"}
	ctx := NewFingerprintContext("gemma2:9b", "gemma2:9b")

	fp1 := b.ComputeFingerprint([]string{"clip-a", "clip-b"}, pack, opts, ctx)
	fp2 := b.ComputeFingerprint([]string{"clip-b", "clip-a"}, pack, opts, ctx)
	if fp1 != fp2 {
		t.Errorf("fingerprint should be order-independent: %q vs %q", fp1, fp2)
	}
}

func TestComputeFingerprint_ChangesOnClipIDChange(t *testing.T) {
	b := makeFingerprintBuilder()
	pack := makeFingerprintPack()
	opts := &ClipGenerationOptions{Language: "en", Tone: "comedy"}
	ctx := NewFingerprintContext("gemma2:9b", "gemma2:9b")

	fp1 := b.ComputeFingerprint([]string{"clip-a"}, pack, opts, ctx)
	fp2 := b.ComputeFingerprint([]string{"clip-c"}, pack, opts, ctx)
	if fp1 == fp2 {
		t.Errorf("fingerprint should change when clip IDs change: %q", fp1)
	}
}

func TestComputeFingerprint_ChangesOnTypeChange(t *testing.T) {
	b := makeFingerprintBuilder()
	pack := makeFingerprintPack()
	ctx := NewFingerprintContext("gemma2:9b", "gemma2:9b")

	doc := &ClipGenerationOptions{Language: "en", Tone: "comedy", Type: "documentary"}
	comp := &ClipGenerationOptions{Language: "en", Tone: "comedy", Type: "compilation"}

	fpDoc := b.ComputeFingerprint([]string{"clip-a"}, pack, doc, ctx)
	fpComp := b.ComputeFingerprint([]string{"clip-a"}, pack, comp, ctx)
	if fpDoc == fpComp {
		t.Errorf("fingerprint should change when Type changes: %q", fpDoc)
	}
}

func TestComputeFingerprint_StableFormat(t *testing.T) {
	// Lock down the fingerprint format: 3-char prefix + 32 hex chars.
	b := makeFingerprintBuilder()
	pack := makeFingerprintPack()
	opts := &ClipGenerationOptions{Language: "en", Tone: "comedy"}
	ctx := NewFingerprintContext("gemma2:9b", "gemma2:9b")

	fp := b.ComputeFingerprint([]string{"clip-a"}, pack, opts, ctx)
	if len(fp) != 3+32 {
		t.Errorf("fingerprint length = %d, want 35 (3 prefix + 32 hex)", len(fp))
	}
	if !strings.HasPrefix(fp, "cs_") {
		t.Errorf("fingerprint %q missing 'cs_' prefix", fp)
	}
	for _, c := range fp[3:] {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("fingerprint %q contains non-hex character %q", fp, c)
			break
		}
	}
}

func TestComputeFingerprint_ReusedContextNotMutated(t *testing.T) {
	// A caller that passes the same context to two fingerprints should
	// see the same input on both calls. Previously fillMissing mutated
	// the receiver, which would change the second call's hash.
	b := makeFingerprintBuilder()
	pack := makeFingerprintPack()
	opts := &ClipGenerationOptions{Language: "en", Tone: "comedy"}
	ctx := &FingerprintVersionContext{
		PlannerPromptVersion: "v1",
		WriterModel:          "custom",
	}

	fp1 := b.ComputeFingerprint([]string{"clip-a"}, pack, opts, ctx)
	// Now inspect ctx to confirm it was not mutated
	if ctx.PlannerPromptVersion != "v1" {
		t.Errorf("ctx.PlannerPromptVersion mutated to %q", ctx.PlannerPromptVersion)
	}
	if ctx.WriterModel != "custom" {
		t.Errorf("ctx.WriterModel mutated to %q", ctx.WriterModel)
	}
	fp2 := b.ComputeFingerprint([]string{"clip-a"}, pack, opts, ctx)
	if fp1 != fp2 {
		t.Errorf("reused context produced different fingerprints: %q vs %q", fp1, fp2)
	}
}
