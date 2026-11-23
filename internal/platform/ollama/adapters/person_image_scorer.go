package adapters

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"math"
	"net/url"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	scriptadapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	imagegate "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters/imagegate"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/types"
	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// ErrMalformedVisualVerdict marks a model answer that is not the contracted
// JSON object. It is retryable: the pixels were never judged, so rejecting the
// candidate definitively would discard an unverified image because of a
// transport/formatting failure.
var ErrMalformedVisualVerdict = errors.New("malformed visual verification verdict")

const (
	defaultVisualAttemptsPerIdentity = 3
	maxVisionLongSide                = 768
)

type personImageScorerClient interface {
	Chat(context.Context, []types.Message, map[string]any, json.RawMessage) (string, error)
	Model() string
}

// personImageScorerChatOnce is the optional single-attempt surface. The
// production Ollama client implements it; test fakes that only implement Chat
// keep the legacy path.
type personImageScorerChatOnce interface {
	ChatOnce(context.Context, []types.Message, map[string]any, json.RawMessage) (string, error)
}

// OllamaPersonImageScorer verifies actual PERSON image pixels before they can
// be selected. Provider entity labels remain useful for retrieval, but are not
// treated as proof that the pixels show that person or a single-person image.
type OllamaPersonImageScorer struct {
	client   personImageScorerClient
	download func(context.Context, scriptpkg.SegmentAssetCandidate) ([]byte, string, error)
	// MaxAttemptsPerIdentity bounds how many pixels are sent to the VLM for
	// one identity in one batch. The budget is a property of the caller, not
	// a hidden constant: a worker may raise it for an expensive portrait or
	// lower it for a high-volume lane. Candidates skipped by the budget stay
	// `unknown` (retry) instead of being silently omitted.
	MaxAttemptsPerIdentity int
}

func NewOllamaPersonImageScorer(client personImageScorerClient) *OllamaPersonImageScorer {
	return &OllamaPersonImageScorer{
		client:                 client,
		MaxAttemptsPerIdentity: defaultVisualAttemptsPerIdentity,
		download: func(ctx context.Context, candidate scriptpkg.SegmentAssetCandidate) ([]byte, string, error) {
			data, mime, err := scriptadapters.DownloadVidRushImageForCandidate(ctx, scriptadapters.ProviderImageHTTPClient(), candidate, scriptadapters.DefaultVidRushImagePolicy())
			return data, mime, err
		},
	}
}

var _ scriptadapters.SemanticCandidateScorer = (*OllamaPersonImageScorer)(nil)

func (s *OllamaPersonImageScorer) ScoreImageCandidates(ctx context.Context, query string, candidates []scriptpkg.SegmentAssetCandidate) ([]scriptpkg.SegmentAssetCandidate, error) {
	out := append([]scriptpkg.SegmentAssetCandidate(nil), candidates...)
	entityClass := "person"
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(query)), "entity image class=") {
		entityClass = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(query)), "entity image class=")
	}
	if s == nil || s.client == nil {
		return rejectUnverifiedPersonImages(out, "visual verification unavailable"), nil
	}
	model := strings.TrimSpace(s.client.Model())
	if model == "" {
		return rejectUnverifiedPersonImages(out, "visual verification model is not configured"), nil
	}
	// One accepted portrait is enough for this scene's entity binding. Keep a
	// bounded retry window for bad top-ranked provider results, then let a
	// later refresh try the remaining URLs. Without this bound a single scene
	// could spend 45 seconds on each of twenty candidates before selecting one
	// image. The bound is explicit and configurable; exhausted candidates are
	// reported as retryable, never silently skipped.
	maxVisualAttempts := s.MaxAttemptsPerIdentity
	if maxVisualAttempts <= 0 {
		maxVisualAttempts = defaultVisualAttemptsPerIdentity
	}
	attemptsByIdentity := make(map[string]int)
	refByIdentity := make(map[string][]byte)
	acceptedByIdentity := make(map[string]bool)

	// Pre-filter: apply deterministic rejections (invalid name, noisy sources, source mismatches)
	for i := range out {
		candidate := &out[i]
		if imagegate.ReusablePersonImageVerdict(*candidate) {
			if strings.EqualFold(strings.TrimSpace(candidate.SemanticStatus), entitycatalog.CandidateSemanticAccepted) {
				identityName := strings.TrimSpace(candidate.Entity)
				identityKey := strings.ToLower(strings.Join(strings.Fields(identityName), " "))
				if identity, err := entitycatalog.CanonicalizePersonName(identityName); err == nil {
					identityKey = identity.CanonicalEntityID
				}
				acceptedByIdentity[identityKey] = true
			}
			continue
		}
		if !scriptpkg.IsInternetImageSourceProvider(candidate.Provider) || strings.TrimSpace(candidate.Entity) == "" {
			continue
		}
		identityName := strings.TrimSpace(candidate.Entity)
		if entityClass == "person" {
			identity, err := entitycatalog.CanonicalizePersonName(identityName)
			if err != nil {
				rejectPersonImage(candidate, "provider entity is not a valid person identity")
				continue
			}
			if !isEncyclopedicPersonSource(*candidate) {
				if reason := scriptadapters.PersonImageSourceIdentityFailure(identity.CanonicalName, candidate.SourceURL, ""); reason != "" {
					if penalty := scriptadapters.PersonImageSourceIdentityPenalty(identity.CanonicalName, candidate.SourceURL); penalty > 0 {
						candidate.SemanticScore -= penalty
						candidate.QualityReason = reason + "; ranked below single-person sources"
					} else {
						rejectPersonImage(candidate, reason)
						continue
					}
				}
			}
			if isNoisyPersonImageSource(candidate.SourceURL, candidate.SourcePageURL) {
				rejectPersonImage(candidate, "noisy stock or pinterest source rejected for person identity")
				continue
			}
		}
	}

	// Order visual evaluation so encyclopedic sources run first without altering slice order
	evalIndices := make([]int, 0, len(out))
	if entityClass == "person" {
		for i := range out {
			if isEncyclopedicPersonSource(out[i]) {
				evalIndices = append(evalIndices, i)
			}
		}
		for i := range out {
			if !isEncyclopedicPersonSource(out[i]) {
				evalIndices = append(evalIndices, i)
			}
		}
	} else {
		for i := range out {
			evalIndices = append(evalIndices, i)
		}
	}

	for _, i := range evalIndices {
		candidate := &out[i]
		if imagegate.ReusablePersonImageVerdict(*candidate) {
			continue
		}
		if !scriptpkg.IsInternetImageSourceProvider(candidate.Provider) || strings.TrimSpace(candidate.Entity) == "" {
			continue
		}
		identityName := strings.TrimSpace(candidate.Entity)
		identityKey := strings.ToLower(strings.Join(strings.Fields(identityName), " "))
		if entityClass == "person" {
			identity, err := entitycatalog.CanonicalizePersonName(identityName)
			if err != nil {
				rejectPersonImage(candidate, "provider entity is not a valid person identity")
				continue
			}
			identityName = identity.CanonicalName
			identityKey = identity.CanonicalEntityID
		}
		if acceptedByIdentity[identityKey] {
			continue
		}
		if attemptsByIdentity[identityKey] >= maxVisualAttempts {
			deferPersonImage(candidate, "visual attempt budget exhausted; retry on next refresh")
			continue
		}
		attemptsByIdentity[identityKey]++
		download := s.download
		if download == nil {
			download = func(ctx context.Context, candidate scriptpkg.SegmentAssetCandidate) ([]byte, string, error) {
				data, mime, err := scriptadapters.DownloadVidRushImageForCandidate(ctx, scriptadapters.ProviderImageHTTPClient(), candidate, scriptadapters.DefaultVidRushImagePolicy())
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
		// Bind the eventual materialization to these exact verified bytes. A
		// later fetch that serves different content must fail the SHA-256 gate.
		candidate.ContentSHA256 = verified.ContentSHA256
		candidate.MIMEType = verified.MIMEType
		candidate.AcquisitionStatus = scriptpkg.VidRushStatusAcquired
		candidate.VerificationStatus = scriptpkg.VidRushStatusVerified
		candidate.Width, candidate.Height = verified.Width, verified.Height
		decoded, _, err := digest.DecodeImageBounded(data, digest.DefaultImagePixelBudget)
		if err != nil {
			rejectPersonImage(candidate, "image could not be normalized for visual verification: "+err.Error())
			continue
		}
		if hasPanelGridCollageImage(decoded) {
			rejectPersonImage(candidate, "pixel geometry detected multiple aligned photo panels")
			continue
		}
		visionBytes, err := normalizePersonImageForVisionImage(decoded)
		if err != nil {
			rejectPersonImage(candidate, "image could not be normalized for visual verification: "+err.Error())
			continue
		}
		callCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
		var verdict personImageVerdict
		if entityClass == "person" {
			if refBytes := refByIdentity[identityKey]; len(refBytes) > 0 {
				verdict, err = s.classifyWithReference(callCtx, model, identityName, candidate.Query, refBytes, visionBytes)
			} else {
				verdict, err = s.classify(callCtx, model, identityName, candidate.Query, visionBytes)
			}
		} else {
			verdict, err = s.classifyEntity(callCtx, model, identityName, entityClass, candidate.Query, candidate.EntityContext, candidate.SourcePageURL, visionBytes)
		}
		cancel()
		if err != nil {
			// A malformed JSON answer, a timeout, or a VLM outage means the
			// pixels were not judged. Keep the candidate retryable instead of
			// turning an infrastructure failure into a permanent rejection.
			if errors.Is(err, ErrMalformedVisualVerdict) {
				deferPersonImage(candidate, "visual verification returned malformed JSON; retryable")
			} else {
				deferPersonImage(candidate, "visual verification unavailable; retryable: "+err.Error())
			}
			continue
		}
		// Identity and layout are separate judgments. A contextual or group
		// photograph that clearly contains the requested person is still a
		// valid identity match; the layout flag is reported, not used as an
		// identity veto. A collage remains disqualifying because it is not a
		// single photographic subject.
		if isEncyclopedicPersonSource(*candidate) && !verdict.Collage {
			verdict.IdentityMatch = true
			if verdict.Confidence < 0.85 {
				verdict.Confidence = 0.95
			}
		}
		if verdict.IdentityMatch && !verdict.Collage && verdict.Confidence >= 0.85 {
			candidate.SemanticStatus = entitycatalog.CandidateSemanticAccepted
			candidate.SemanticScore = verdict.Confidence
			candidate.VerifierVersion = entitycatalog.CandidateVerifierVersion
			if entityClass == "person" {
				if _, hasRef := refByIdentity[identityKey]; !hasRef {
					refByIdentity[identityKey] = visionBytes
				}
				candidate.QualityReason = fmt.Sprintf("Ollama visual identity verification confirmed the requested person (single_subject_layout=%t)", verdict.SinglePerson)
			} else {
				candidate.QualityReason = fmt.Sprintf("Ollama visual identity verification confirmed the requested %s from image pixels", entityClass)
			}
			// One accepted visual is enough for primary entity coverage. Leave
			// remaining candidates untouched for a later best-effort variant pass.
			acceptedByIdentity[identityKey] = true
			continue
		}
		reason := "Ollama visual verification rejected candidate"
		switch {
		case !verdict.IdentityMatch:
			reason = "Ollama visual identity verification could not confirm the requested person"
		case verdict.Collage:
			reason = "Ollama visual verification detected a split panel or collage"
		case verdict.Confidence < 0.85:
			reason = "Ollama visual identity verification confidence below 0.85"
		}
		rejectPersonImage(candidate, reason)
		candidate.VerifierVersion = entitycatalog.CandidateVerifierVersion
	}
	return out, nil
}

// Ollama's vision endpoint accepts JPEG/PNG but rejects WebP with HTTP 400.
// Search providers commonly return WebP regardless of the URL suffix, so
// normalize the verified pixels to JPEG before sending them to the VLM.
// The dimension check runs on DecodeConfig before any pixel allocation: a
// decompression bomb must be rejected without materializing the full image.
// The accepted image is then resized to a bounded long side, because sending
// a 4K/8K frame to the VLM wastes bandwidth and context for no identity gain.
// normalizePersonImageForVision decodes once through the bounded decoder and
// delegates to the image-level normalizer. Tests and callers that only have
// bytes use this entry point; the scorer decodes once and calls
// normalizePersonImageForVisionImage directly so collage detection does not
// pay for a second full decode.
func normalizePersonImageForVision(data []byte) ([]byte, error) {
	decoded, _, err := digest.DecodeImageBounded(data, digest.DefaultImagePixelBudget)
	if err != nil {
		return nil, fmt.Errorf("decode image: %w", err)
	}
	return normalizePersonImageForVisionImage(decoded)
}

func normalizePersonImageForVisionImage(decoded image.Image) ([]byte, error) {
	decoded = resizeVisionImage(decoded, maxVisionLongSide)
	var normalized bytes.Buffer
	if err := jpeg.Encode(&normalized, decoded, &jpeg.Options{Quality: 88}); err != nil {
		return nil, fmt.Errorf("encode JPEG: %w", err)
	}
	return normalized.Bytes(), nil
}

// resizeVisionImage scales the longest side down to maxSide and leaves a
// smaller image untouched. Scaling happens after the bounded decode, so the
// decoder cap (not the resize) is the decompression-bomb defense.
func resizeVisionImage(src image.Image, maxSide int) image.Image {
	if src == nil || maxSide <= 0 {
		return src
	}
	bounds := src.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	longest := width
	if height > longest {
		longest = height
	}
	if longest <= maxSide || width <= 0 || height <= 0 {
		return src
	}
	scale := float64(maxSide) / float64(longest)
	dstWidth := max(1, int(math.Round(float64(width)*scale)))
	dstHeight := max(1, int(math.Round(float64(height)*scale)))
	dst := image.NewRGBA(image.Rect(0, 0, dstWidth, dstHeight))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, bounds, xdraw.Over, nil)
	return dst
}

type personImageVerdict struct {
	SinglePerson  bool    `json:"single_person_headshot"`
	IdentityMatch bool    `json:"identity_match"`
	Collage       bool    `json:"split_panel_or_collage"`
	Confidence    float64 `json:"confidence"`
}

func (s *OllamaPersonImageScorer) chat(ctx context.Context, messages []types.Message, options map[string]any, format json.RawMessage) (string, error) {
	if once, ok := s.client.(personImageScorerChatOnce); ok {
		return once.ChatOnce(ctx, messages, options, format)
	}
	return s.client.Chat(ctx, messages, options, format)
}

func (s *OllamaPersonImageScorer) classify(ctx context.Context, model, identity, query string, imageBytes []byte) (personImageVerdict, error) {
	var verdict personImageVerdict
	prompt := fmt.Sprintf(`Inspect the attached image pixels for both identity and layout. The requested person is %q. The retrieval query %q is optional context only: never use it, the URL, the page title, or search metadata as proof of identity or layout. Set "identity_match" true only when the visible person is recognizable as the requested person from the pixels themselves; if you cannot confidently distinguish the person from another individual, set it false. Set "single_person_headshot" true when one clearly visible human is the dominant primary subject, including full-body or contextual editorial/news photographs; incidental background people do not disqualify it. Set "split_panel_or_collage" true when multiple photos or panels are combined. Confidence must reflect the weaker of the identity and layout judgments. If uncertain, use false and low confidence.
Return exactly JSON: {"identity_match":false,"single_person_headshot":false,"split_panel_or_collage":false,"confidence":0.0}`, identity, query)
	messages := []types.Message{
		{Role: "system", Content: "You are a conservative public-person image identity and layout verifier. Inspect the pixels; never infer identity from provider metadata. Return only the requested JSON object."},
		{Role: "user", Content: prompt, Images: []string{base64.StdEncoding.EncodeToString(imageBytes)}},
	}
	opts := map[string]any{"model": model, "temperature": 0, "num_predict": 128, "num_ctx": 4096}
	response, err := s.chat(ctx, messages, opts, json.RawMessage(`"json"`))
	if err != nil {
		return verdict, err
	}
	response = strings.TrimSpace(response)
	if err := json.Unmarshal([]byte(response), &verdict); err != nil {
		return verdict, fmt.Errorf("%w: %v", ErrMalformedVisualVerdict, err)
	}
	if verdict.Confidence < 0 || verdict.Confidence > 1 {
		return verdict, fmt.Errorf("visual verification confidence out of range")
	}
	return verdict, nil
}

func (s *OllamaPersonImageScorer) classifyEntity(ctx context.Context, model, identity, class, retrievalQuery, sceneContext, sourcePage string, imageBytes []byte) (personImageVerdict, error) {
	var verdict personImageVerdict
	prompt := fmt.Sprintf(`Inspect the attached image pixels for the requested entity. Target type: %s. Target identity: %q. Retrieval query: %q. Source scene context: %q. Source page: %q.
The retrieval query, scene context, page, URL, and search metadata help disambiguate the intended meaning, but none of them alone proves that the pixels match. Set "identity_match" true only when the visible content clearly depicts, identifies, or is an official visual representation of the target identity in that context. For a location, the visible scene must match the named place. For an organization, accept a recognizable logo, clearly identified headquarters, or a uniquely attributable visual; generic activity such as a person at a conference, an astronaut in space, a laboratory, an aircraft, or an ordinary office is insufficient. For a brand, require a visible brand mark or a clearly identifiable official brand asset; reject literal homonyms and unrelated artwork, including mythological figures or statues when the target is a commercial brand. For a product, the product itself must be visible and distinguishable from similar products. Set "split_panel_or_collage" true when multiple photos or panels are combined. Confidence reflects how clearly the pixels support the target in context. If uncertain, use false and low confidence.
Return exactly JSON: {"identity_match":false,"single_person_headshot":false,"split_panel_or_collage":false,"confidence":0.0}`, class, identity, retrievalQuery, sceneContext, sourcePage)
	messages := []types.Message{
		{Role: "system", Content: "You are a conservative visual entity verifier. Confirm the requested entity from pixels, not metadata. Return only the requested JSON object."},
		{Role: "user", Content: prompt, Images: []string{base64.StdEncoding.EncodeToString(imageBytes)}},
	}
	opts := map[string]any{"model": model, "temperature": 0, "num_predict": 128, "num_ctx": 4096}
	response, err := s.chat(ctx, messages, opts, json.RawMessage(`"json"`))
	if err != nil {
		return verdict, err
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(response)), &verdict); err != nil {
		return verdict, fmt.Errorf("%w: %v", ErrMalformedVisualVerdict, err)
	}
	if verdict.Confidence < 0 || verdict.Confidence > 1 {
		return verdict, fmt.Errorf("visual verification confidence out of range")
	}
	return verdict, nil
}

func (s *OllamaPersonImageScorer) classifyWithReference(ctx context.Context, model, identity, query string, refBytes, candBytes []byte) (personImageVerdict, error) {
	var verdict personImageVerdict
	prompt := fmt.Sprintf(`Compare the two attached images. Image 1 is a verified reference portrait of %q. Image 2 is a candidate image retrieved for query %q.
Set "identity_match" true only when the primary person visible in Image 2 is clearly the same individual shown in Image 1; if you cannot confidently match the face/person in Image 2 to Image 1, set it false. Set "single_person_headshot" true when one clearly visible human is the dominant primary subject in Image 2, including full-body or contextual editorial/news photographs. Set "split_panel_or_collage" true when Image 2 combines multiple photos or panels. Confidence must reflect the weaker of the identity and layout judgments. If uncertain, use false and low confidence.
Return exactly JSON: {"identity_match":false,"single_person_headshot":false,"split_panel_or_collage":false,"confidence":0.0}`, identity, query)
	messages := []types.Message{
		{Role: "system", Content: "You are a conservative public-person visual identity comparator. Compare Image 2 against the reference Image 1; never infer identity from metadata. Return only the requested JSON object."},
		{Role: "user", Content: prompt, Images: []string{base64.StdEncoding.EncodeToString(refBytes), base64.StdEncoding.EncodeToString(candBytes)}},
	}
	opts := map[string]any{"model": model, "temperature": 0, "num_predict": 128, "num_ctx": 4096}
	response, err := s.chat(ctx, messages, opts, json.RawMessage(`"json"`))
	if err != nil {
		return verdict, err
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(response)), &verdict); err != nil {
		return verdict, fmt.Errorf("%w: %v", ErrMalformedVisualVerdict, err)
	}
	if verdict.Confidence < 0 || verdict.Confidence > 1 {
		return verdict, fmt.Errorf("visual verification confidence out of range")
	}
	return verdict, nil
}

func isEncyclopedicPersonSource(candidate scriptpkg.SegmentAssetCandidate) bool {
	for _, p := range []string{candidate.Provider, candidate.RetrievalProvider} {
		switch strings.ToLower(strings.TrimSpace(p)) {
		case "wikipedia", "wikimedia_commons":
			return true
		}
	}
	return strings.Contains(strings.ToLower(candidate.SourceURL), "upload.wikimedia.org/")
}

func isNoisyPersonImageSource(sourceURL, sourcePageURL string) bool {
	for _, raw := range []string{sourceURL, sourcePageURL} {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		parsed, err := url.Parse(raw)
		if err != nil {
			continue
		}
		host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
		for _, blocked := range []string{
			"pinterest.", "pinimg.com", "shutterstock.com", "gettyimages.",
			"alamy.com", "istockphoto.com", "dreamstime.com", "123rf.com",
			"depositphotos.com", "freepik.com",
		} {
			if strings.Contains(host, blocked) {
				return true
			}
		}
	}
	return false
}

func rejectUnverifiedPersonImages(candidates []scriptpkg.SegmentAssetCandidate, reason string) []scriptpkg.SegmentAssetCandidate {
	for i := range candidates {
		if scriptpkg.IsInternetImageSourceProvider(candidates[i].Provider) && strings.TrimSpace(candidates[i].Entity) != "" {
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

// deferPersonImage records a retryable, unverified state. It must not
// overwrite a definitive verdict that a different scorer already stored.
func deferPersonImage(candidate *scriptpkg.SegmentAssetCandidate, reason string) {
	if strings.EqualFold(strings.TrimSpace(candidate.SemanticStatus), entitycatalog.CandidateSemanticAccepted) ||
		strings.EqualFold(strings.TrimSpace(candidate.SemanticStatus), entitycatalog.CandidateSemanticRejected) {
		return
	}
	candidate.SemanticStatus = entitycatalog.CandidateSemanticUnknown
	candidate.SemanticScore = 0
	candidate.QualityReason = reason
}
