package imageverify

import (
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

var (
	ErrImageTooLarge = errors.New("vidrush image exceeds download limit")
	ErrImageInvalid  = errors.New("vidrush image is not a valid supported image")
	// ErrImageCollage marks a contact sheet, montage or triptych. It wraps
	// ErrImageInvalid so every soft-reject path keeps working, while the
	// reason stays precise: one "photo" that shows several pictures reads to
	// the viewer as stacked images, so the candidate must not bind.
	ErrImageCollage = fmt.Errorf("%w: panel grid collage", ErrImageInvalid)
)

// VerifyBytes performs the deterministic technical checks after acquisition.
// It does not assert rights: rights are a separate policy input.
func VerifyBytes(candidate scriptpkg.SegmentAssetCandidate, data []byte, mime string, policy Policy) (scriptpkg.VerifiedArtifact, error) {
	policy = NormalizePolicy(policy)
	if len(data) == 0 {
		return scriptpkg.VerifiedArtifact{}, ErrImageInvalid
	}
	if int64(len(data)) > policy.MaxBytes {
		return scriptpkg.VerifiedArtifact{}, ErrImageTooLarge
	}
	if !allowedMIME(mime) {
		return scriptpkg.VerifiedArtifact{}, ErrImageInvalid
	}
	config, _, err := digest.DecodeImageConfigBounded(data, policy.MaxPixels)
	if err != nil {
		if errors.Is(err, digest.ErrImagePixelBudget) {
			return scriptpkg.VerifiedArtifact{}, ErrImageTooLarge
		}
		return scriptpkg.VerifiedArtifact{}, ErrImageInvalid
	}
	if !policy.AcceptsDimensions(config.Width, config.Height) {
		return scriptpkg.VerifiedArtifact{}, ErrImageInvalid
	}
	img, _, err := digest.DecodeImageBounded(data, policy.MaxPixels)
	if err != nil {
		if errors.Is(err, digest.ErrImagePixelBudget) {
			return scriptpkg.VerifiedArtifact{}, ErrImageTooLarge
		}
		return scriptpkg.VerifiedArtifact{}, ErrImageInvalid
	}
	if digest.HasPanelGridCollageImage(img) {
		seamsV, seamsH, continuityV, continuityH := digest.PanelGridCollageEvidence(img)
		log.Printf("vidrush image rejected as panel grid collage: seams_v=%d seams_h=%d continuity_v=%.2f continuity_h=%.2f",
			seamsV, seamsH, continuityV, continuityH)
		return scriptpkg.VerifiedArtifact{}, ErrImageCollage
	}
	sum := digest.SHA256Bytes(data)
	candidate.ContentSHA256 = sum
	candidate.MIMEType = mime
	candidate.Width = config.Width
	candidate.Height = config.Height
	// The perceptual hash is best-effort: it powers near-duplicate detection
	// across providers, but a decoder that cannot produce one must not fail
	// technical verification.
	if perceptualHash, hashErr := digest.PerceptualHash(img); hashErr == nil {
		candidate.PerceptualHash = perceptualHash
	}
	candidate.AcquisitionStatus = scriptpkg.VidRushStatusAcquired
	candidate.VerificationStatus = scriptpkg.VidRushStatusVerified
	return scriptpkg.VerifiedArtifact{
		Candidate:     candidate,
		MIMEType:      mime,
		SizeBytes:     int64(len(data)),
		ContentSHA256: candidate.ContentSHA256,
		Width:         config.Width,
		Height:        config.Height,
		RightsStatus:  candidate.RightsStatus,
	}, nil
}

func allowedMIME(mime string) bool {
	switch strings.ToLower(strings.TrimSpace(strings.Split(mime, ";")[0])) {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		return true
	default:
		return false
	}
}
