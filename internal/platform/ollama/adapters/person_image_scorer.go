package adapters

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"net/http"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	scriptadapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/types"
	_ "golang.org/x/image/webp"
)

type personImageScorerClient interface {
	Chat(context.Context, []types.Message, map[string]any, json.RawMessage) (string, error)
	Model() string
}

// OllamaPersonImageScorer verifies actual PERSON image pixels before they can
// be selected. Provider entity labels remain useful for retrieval, but are not
// treated as proof that the pixels show that person or a single-person image.
type OllamaPersonImageScorer struct {
	client   personImageScorerClient
	download func(context.Context, scriptpkg.SegmentAssetCandidate) ([]byte, string, error)
}

func NewOllamaPersonImageScorer(client personImageScorerClient) *OllamaPersonImageScorer {
	return &OllamaPersonImageScorer{
		client: client,
		download: func(ctx context.Context, candidate scriptpkg.SegmentAssetCandidate) ([]byte, string, error) {
			data, mime, err := scriptadapters.DownloadVidRushImageForCandidate(ctx, http.DefaultClient, candidate, scriptadapters.DefaultVidRushImagePolicy())
			return data, mime, err
		},
	}
}

var _ scriptadapters.SemanticCandidateScorer = (*OllamaPersonImageScorer)(nil)

func (s *OllamaPersonImageScorer) ScoreImageCandidates(ctx context.Context, query string, candidates []scriptpkg.SegmentAssetCandidate) ([]scriptpkg.SegmentAssetCandidate, error) {
	out := append([]scriptpkg.SegmentAssetCandidate(nil), candidates...)
	if s == nil || s.client == nil {
		return rejectUnverifiedPersonImages(out, "visual verification unavailable"), nil
	}
	model := strings.TrimSpace(s.client.Model())
	if model == "" {
		return rejectUnverifiedPersonImages(out, "visual verification model is not configured"), nil
	}
	for i := range out {
		candidate := &out[i]
		if scriptadapters.ReusablePersonImageVerdict(*candidate) {
			// The verdict is already stored on the candidate/catalog. Do not
			// download the pixels or spend a VLM call again merely because a
			// later stage received the same cached candidate.
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(candidate.Provider), scriptpkg.VidRushProviderInternetImages) || strings.TrimSpace(candidate.Entity) == "" {
			continue
		}
		identity, err := entitycatalog.CanonicalizePersonName(candidate.Entity)
		if err != nil {
			rejectPersonImage(candidate, "provider entity is not a valid person identity")
			continue
		}
		if reason := scriptadapters.PersonImageSourceIdentityFailure(identity.CanonicalName, candidate.SourceURL, ""); reason != "" {
			rejectPersonImage(candidate, reason)
			continue
		}
		download := s.download
		if download == nil {
			download = func(ctx context.Context, candidate scriptpkg.SegmentAssetCandidate) ([]byte, string, error) {
				data, mime, err := scriptadapters.DownloadVidRushImageForCandidate(ctx, http.DefaultClient, candidate, scriptadapters.DefaultVidRushImagePolicy())
				return data, mime, err
			}
		}
		data, mime, err := download(ctx, *candidate)
		if err != nil {
			rejectPersonImage(candidate, "image could not be downloaded for visual verification")
			continue
		}
		verified, err := scriptadapters.VerifyVidRushImageBytes(*candidate, data, mime, scriptadapters.DefaultVidRushImagePolicy())
		if err != nil {
			rejectPersonImage(candidate, "technical image verification rejected candidate: "+err.Error())
			continue
		}
		candidate.Width, candidate.Height = verified.Width, verified.Height
		if hasPanelGridCollage(data) {
			rejectPersonImage(candidate, "pixel geometry detected multiple aligned photo panels")
			continue
		}
		visionBytes, err := normalizePersonImageForVision(data)
		if err != nil {
			rejectPersonImage(candidate, "image could not be normalized for visual verification: "+err.Error())
			continue
		}
		callCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
		verdict, err := s.classify(callCtx, model, identity.CanonicalName, query, visionBytes)
		cancel()
		if err != nil {
			rejectPersonImage(candidate, "visual verification failed: "+err.Error())
			continue
		}
		if verdict.SinglePerson && !verdict.Collage && verdict.Confidence >= 0.75 {
			candidate.SemanticStatus = entitycatalog.CandidateSemanticAccepted
			candidate.SemanticScore = verdict.Confidence
			candidate.QualityReason = "canonical identity appears in source URL; Ollama visual verification confirmed one dominant subject and no collage"
			continue
		}
		reason := "Ollama visual verification rejected candidate"
		switch {
		case verdict.Collage:
			reason = "Ollama visual verification detected a split panel or collage"
		case !verdict.SinglePerson:
			reason = "Ollama visual verification did not find one clear person"
		case verdict.Confidence < 0.75:
			reason = "Ollama visual verification confidence below 0.75"
		}
		rejectPersonImage(candidate, reason)
	}
	return out, nil
}

// Ollama's vision endpoint accepts JPEG/PNG but rejects WebP with HTTP 400.
// Search providers commonly return WebP regardless of the URL suffix, so
// normalize the verified pixels to JPEG before sending them to the VLM.
func normalizePersonImageForVision(data []byte) ([]byte, error) {
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode image: %w", err)
	}
	var normalized bytes.Buffer
	if err := jpeg.Encode(&normalized, decoded, &jpeg.Options{Quality: 88}); err != nil {
		return nil, fmt.Errorf("encode JPEG: %w", err)
	}
	return normalized.Bytes(), nil
}

type personImageVerdict struct {
	SinglePerson bool    `json:"single_person_headshot"`
	Collage      bool    `json:"split_panel_or_collage"`
	Confidence   float64 `json:"confidence"`
}

func (s *OllamaPersonImageScorer) classify(ctx context.Context, model, identity, query string, imageBytes []byte) (personImageVerdict, error) {
	var verdict personImageVerdict
	prompt := fmt.Sprintf(`Inspect only the attached image pixels for subject layout. The durable image URL contains the requested canonical name %q; do not try to identify the person's face or infer identity from search context %q.
Return a conservative image-layout verdict. "single_person_headshot" is true when one clearly visible human is the dominant primary subject, even in a full-body or contextual editorial/news photograph; incidental background people, reporters, camera operators, or passersby do not disqualify it when they are not co-primary subjects. Do not require a tight headshot crop. It is false when multiple people are equally prominent foreground subjects, the person is too small or unclear, or no clear primary person is visible. "split_panel_or_collage" is true for multiple photos or panels combined into one image. If uncertain, use false and low confidence.
Return exactly JSON: {"single_person_headshot":false,"split_panel_or_collage":false,"confidence":0.0}`, identity, query)
	messages := []types.Message{
		{Role: "system", Content: "You are a strict image quality verifier. Inspect image pixels, ignore provider metadata, and do not guess identities. Return only the requested JSON object."},
		{Role: "user", Content: prompt, Images: []string{base64.StdEncoding.EncodeToString(imageBytes)}},
	}
	opts := map[string]any{"model": model, "temperature": 0, "num_predict": 128, "num_ctx": 4096}
	response, err := s.client.Chat(ctx, messages, opts, json.RawMessage(`"json"`))
	if err != nil {
		return verdict, err
	}
	response = strings.TrimSpace(response)
	if err := json.Unmarshal([]byte(response), &verdict); err != nil {
		return verdict, fmt.Errorf("parse visual verification JSON: %w", err)
	}
	if verdict.Confidence < 0 || verdict.Confidence > 1 {
		return verdict, fmt.Errorf("visual verification confidence out of range")
	}
	return verdict, nil
}

func rejectUnverifiedPersonImages(candidates []scriptpkg.SegmentAssetCandidate, reason string) []scriptpkg.SegmentAssetCandidate {
	for i := range candidates {
		if strings.EqualFold(strings.TrimSpace(candidates[i].Provider), scriptpkg.VidRushProviderInternetImages) && strings.TrimSpace(candidates[i].Entity) != "" {
			rejectPersonImage(&candidates[i], reason)
		}
	}
	return candidates
}

func rejectPersonImage(candidate *scriptpkg.SegmentAssetCandidate, reason string) {
	candidate.SemanticStatus = entitycatalog.CandidateSemanticRejected
	candidate.SemanticScore = 0
	candidate.QualityReason = reason
}
