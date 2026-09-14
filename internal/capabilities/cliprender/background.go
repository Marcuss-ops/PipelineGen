// Package cliprender — background.go
//
// The background media-family contract: which of the two renderable plate
// families a background asset belongs to, and who decides.
//
// Why this is its own file. The family is a RENDER decision disguised as an
// asset attribute: an image plate and a video plate are sampled by two
// different layers of the render plan, so a plan that cannot name the family is
// not renderable. Before this contract every producer hardcoded a video plate,
// which made an image background silently render as a video source. Keeping the
// enum, the taxonomy projection and the resolver together gives that decision
// exactly one owner instead of one spelling per call site.
package cliprender

import (
	"fmt"
	"strings"

	assetpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// Background asset KINDS. They mirror the renderer's layer vocabulary verbatim
// (image | video): a kind invented here that the renderer does not know would
// be rejected at the boundary, so the two spellings are one value. The kind is
// a real render decision — an image plate is sampled once and stays
// GPU-resident, a video plate is a second decoded source.
const (
	BackgroundKindImage = "image"
	BackgroundKindVideo = "video"
)

// IsBackgroundKind reports whether value is a canonical background kind.
// Comparison is exact: a near miss ("images", "VIDEO ") is a producer bug and
// must not be silently normalised into a render decision.
func IsBackgroundKind(value string) bool {
	switch value {
	case BackgroundKindImage, BackgroundKindVideo:
		return true
	}
	return false
}

// BackgroundKindFromMediaType projects the canonical asset MediaType taxonomy
// onto the render vocabulary (image | video). It is the ONE owner of "what
// media family is this background asset": the preparer derives the sealed
// plan's kind here rather than at each of the (media) catalogs that spell the
// type differently (kernel/asset uses image|clip|stock|image_video, the media
// DB also stores the transport spelling video|image).
//
// ok=false means the asset is not a renderable background plate (audio,
// document, script, empty): the caller fails closed instead of rendering an
// audio file as a background.
func BackgroundKindFromMediaType(mediaType string) (kind string, ok bool) {
	mt := strings.ToLower(strings.TrimSpace(mediaType))
	// Drop MIME parameters ("image/png; charset=binary").
	if i := strings.IndexByte(mt, ';'); i >= 0 {
		mt = strings.TrimSpace(mt[:i])
	}
	switch assetpkg.MediaType(mt) {
	case assetpkg.MediaTypeImage:
		return BackgroundKindImage, true
	case assetpkg.MediaTypeClip, assetpkg.MediaTypeStock, assetpkg.MediaTypeImageVideo:
		// image_video is a GENERATED VIDEO file (not an image), and is
		// matched before any prefix rule could see its "image" prefix.
		return BackgroundKindVideo, true
	}
	if mt == "video" {
		// Transport spelling used by the media DB rows.
		return BackgroundKindVideo, true
	}
	switch {
	case strings.HasPrefix(mt, "image/"):
		return BackgroundKindImage, true
	case strings.HasPrefix(mt, "video/"):
		return BackgroundKindVideo, true
	}
	return "", false
}

// resolveBackgroundKind resolves the sealed plan's background media family from
// the caller's explicit selection and the resolved asset's canonical type. An
// explicit `background.kind` wins (a caller may know better than a mislabelled
// catalog row); otherwise the MediaType is the authority. It fails closed when
// neither yields a renderable family — a background whose bytes we cannot
// classify must not reach a renderer that would have to guess.
func resolveBackgroundKind(requested string, assetID, mediaType string) (string, error) {
	if explicit := strings.TrimSpace(requested); explicit != "" {
		if !IsBackgroundKind(explicit) {
			return "", fmt.Errorf("%w: background.kind must be one of %s, %s (got %q)", ErrInvalidRequest, BackgroundKindImage, BackgroundKindVideo, requested)
		}
		return explicit, nil
	}
	kind, ok := BackgroundKindFromMediaType(mediaType)
	if !ok {
		return "", fmt.Errorf("%w: background asset %q has media_type %q, which is not a renderable background plate (need %s or %s); set background.kind explicitly only if the catalog type is wrong", ErrInvalidRequest, assetID, mediaType, BackgroundKindImage, BackgroundKindVideo)
	}
	return kind, nil
}
