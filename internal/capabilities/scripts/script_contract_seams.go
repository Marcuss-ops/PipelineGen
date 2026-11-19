// Package scriptgeneration — script_contract_seams.go consolidates the small
// cross-cutting policy seams that were previously one file each. Ownership and
// behavior are unchanged; the consolidation reduces same-package micro-files.
package scriptgeneration

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/buildinfo"
)

// attachBuildIdentity stamps the durable result with the executable digest and
// the BrainRust release it consumed. Both fields are observable in the job
// report, so a result can never be attributed to an unknown binary.
func attachBuildIdentity(result *GenerateResult) {
	if result == nil {
		return
	}
	identity := buildinfo.Current()
	result.BinarySHA256 = strings.TrimSpace(identity.BinarySHA256)
	result.RustRelease = currentRustRelease()
}

func currentRustRelease() string {
	if release := strings.TrimSpace(os.Getenv("BRAINRUST_RELEASE")); release != "" {
		return release
	}
	data, err := os.ReadFile(filepath.Join("bin", ".rust-release"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "version=") {
			return strings.TrimSpace(strings.TrimPrefix(line, "version="))
		}
	}
	return ""
}

// vidRushEntityImageOwnsDiscovery names the one owner of entity-image query
// generation, candidate discovery and selection for a plan.
//
// The VidRush entity-image lane owns those decisions when all of these hold:
// the internet-images provider is enabled, the entity-image surface is
// enabled, the entity-image scope is per-scene, and image search is not
// disabled. In that mode the semantic production plan must exit: running both
// flows against the same identities lets two different
// query/candidate/verdict/selection pipelines disagree.
//
// EntityImages.Enabled is required explicitly: PerScene() is derived from the
// scope alone, so a plan that switched the entity surface off but left the
// scope at "per_scene" would otherwise suppress the semantic batch and leave
// the run with no discovery owner at all.
//
// The semantic plan remains the fallback for overlay-only plans (entity scope
// without per-scene images), where the VidRush lane does not run.
func vidRushEntityImageOwnsDiscovery(plan mediadomain.MediaPlanSpec) bool {
	if !plan.ProviderPolicy.InternetImages.AsBool() {
		return false
	}
	if !plan.Extraction.EntityImages.Enabled {
		return false
	}
	if !plan.Extraction.EntityImages.PerScene() {
		return false
	}
	return plan.Extraction.ImageSearchMode != mediadomain.ImageSearchModeDisabled
}

// EntityImageDecisionOwner names the single flow allowed to decide which
// entity image is selected and bound for this run. The other flow may still
// produce diagnostics, but it does not write a competing binding.
type EntityImageDecisionOwner string

const (
	// EntityImageDecisionSemanticPlanner is the fallback owner when the media
	// pipeline does not request an entity-image surface. The semantic plan may
	// bind verified, content-addressed hits.
	EntityImageDecisionSemanticPlanner EntityImageDecisionOwner = "semantic_planner"
	// EntityImageDecisionMediaPipeline is the owner whenever the run requests
	// an entity-image surface. Discovery, verdicts, selection and binding all
	// belong to the media pipeline/catalog; the semantic plan is a diagnostic
	// producer and must not bind a second image.
	EntityImageDecisionMediaPipeline EntityImageDecisionOwner = "media_pipeline"
)

// ResolveEntityImageDecisionOwner is the one ownership rule for a run. It is
// deliberately based on the requested surface, not on whether a provider
// happened to return a hit: a fallback must not become an owner merely because
// the primary flow missed.
func ResolveEntityImageDecisionOwner(plan mediadomain.MediaPlanSpec) EntityImageDecisionOwner {
	if plan.Extraction.EntityImageSurfaceEnabled() {
		return EntityImageDecisionMediaPipeline
	}
	return EntityImageDecisionSemanticPlanner
}

// freezeTailedVideoSegments returns the scene's visual segments, appending a
// synthetic freeze tail when the canonical scene duration outlasts the real
// clips (a narration longer than its clip). The tail holds the last clip's
// final frame for the remaining window, so the canonical timeline's video
// always covers the scene. This is the single owner of the freeze policy —
// never an implicit black gap guessed by the renderer.
func freezeTailedVideoSegments(scene ResolvedScene, durationUS int64) []audio.VideoSegment {
	videos := scene.VideoSegments
	if len(videos) == 0 && scene.Video.AssetID != "" {
		videos = []audio.VideoSegment{scene.Video}
	}
	if len(videos) == 0 {
		return nil
	}
	var visualEnd int64
	for _, v := range videos {
		visualEnd += v.TimelineDurationUS
	}
	if visualEnd >= durationUS {
		return videos
	}
	last := videos[len(videos)-1]
	return append(append([]audio.VideoSegment(nil), videos...), audio.VideoSegment{
		AssetID:            last.AssetID,
		SourceInUS:         last.SourceInUS + last.SourceDurationUS, // exclusive source end
		TimelineOffsetUS:   visualEnd,
		TimelineDurationUS: durationUS - visualEnd,
		Freeze:             true,
	})
}

// recordSceneStage records one owner-measured (scene, stage) lifecycle on the
// Run bound to ctx; it is a no-op when no Run is bound (tests, dry runs). A
// non-nil err marks the entry failed with its stable error code.
func recordSceneStage(ctx context.Context, sceneID string, stage kernobs.SceneStage, times kernobs.SceneStageTimes, language string, err error) {
	kernobs.RecordSceneStage(ctx, sceneID, stage, times, language, "", err)
}
