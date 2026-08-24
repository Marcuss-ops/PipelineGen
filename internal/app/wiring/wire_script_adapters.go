// Package app — wire_script_adapters.go.
//
// FASE 2.A PR3 (June 2026) split: the infrastructure-bridging
// adapter types + the composition-time wiring validators moved out of
// wire_script.go. The previous PR3 file (wire_script.go at pre-PR3
// 698 LOC) interleaved five responsibilities: source resolvers
// (now in wire_script_sources.go), curation adapters (now in
// wire_script_curation.go), post-processor registration (now in
// wire_script_postprocess.go), and the two responsibilities
// collected here — concrete port adapters + composition invariants.
//
//  1. validateScriptGenerateWiring + validateRequiredProcessors +
//     requiredProcessorNames — these are composition-time invariants
//     that gate fail-closed on missing components. Issue 7 / P1
//     (June 2026) replaced the pre-Issue-7 log.Warn with explicit
//     composition-time errors; PR 2 (June 2026) closed the
//     "partial registration" gap with the post-freeze required-names
//     check. Grouping them with the adapter types is intentional:
//     both are infrastructure-bridging concerns (adapters bridge
//     concrete services into typed ports; validators bridge
//     composition-time state into fail-closed startup semantics).
//
// Package boundary: same `package app` as wire_script.go. Promoting
// either cluster to a sub-package would force wire_script.go to
// import a new symbol while preserving the same constructor
// call-site; staying in `package app` matches the
// clips_adapters_*.go + adapters_infra.go convention already in
// use across the composition root.
//
// Cross-references:
//   - internal/app/wire_script.go: the caller (wireScriptFlow invokes
//     validateScriptGenerateWiring after job registration).
//   - internal/app/wire_script_postprocess.go: registerScriptPostProcessors
//     populates the ppReg that validateRequiredProcessors scans.
//   - internal/capabilities/jobs/queue: appjobs.Compose() (the typed
//     job-type registry queried by validateScriptGenerateWiring).
//   - internal/domain/job: job.TypeScriptGenerate (the canonical
//     job-type ID validated in step (a) of the 3-invariant check).
//   - internal/kernel/script: scriptpkg.PlanInvalidError (the
//     typed error returned from validateRequiredProcessors).
//   - internal/application/scripts/adapters: PostProcessorRegistry
//   - ProcessorRequired policy classification (the validator's
//     scanning surface).
package wiring

import (
	"context"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"math/rand"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providerassets"
	imagesrouting "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	adapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/usecase"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	sqassets "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/channels"

	"go.uber.org/zap"
)

// ── Composition validation: script.generate wiring must be complete ───

// validateScriptGenerateWiring enforces the 3 canonical invariants
// for `script.generate` to be considered ready for production
// traffic. Issues 7 / P1 (June 2026): the pre-Issue-7 wireScriptFlow
// only log.Warn'd on missing broker / registration failure, which
// silently let the server come up without a working
// script.generate handler. Composition must fail closed so the
// operator sees a clear restart-required message instead of a
// runtime regression.
//
// The 3 invariants:
//
//	(a) Registry has the type. Looks up appjobs.Compose().IsRegistered
//	    for script.generate -- the canonical job-type registry built
//	    in module_media.go::BuildJobsBundle.
//
//	(b) Broker has the handler. The handler-registration itself is
//	    the proof: RegisterJobs just successfully pushed the handler
//	    into the broker. A nil Jobs service at this point means the
//	    gate at line ~N (above) should have already tripped -- the
//	    explicit re-check here is defense in depth.
//
//	(c) At least one worker in the cluster is configured to claim
//	    script.generate jobs. The cluster may advertise the
//	    worker-types list via root.Jobs.WorkerTypes (forward-looking
//	    field; nil-tolerant while clusters in-flight don't expose
//	    it). When the list is exposed and script.generate is missing,
//	    the validator surfaces it; when the list is nil (legacy /
//	    cluster not yet exposing WorkerTypes), the check is skipped
//	    and operators must rely on the canonical worker.ExportTypes
//	    audit at runtime.
//
// Returns the FIRST failing invariant as a typed wireScriptFlow
// error so the composition root can wrap it consistently with the
// other composition validators (validateRequiredProcessors,
// etc.). Tests pin the fail-fast contract in
// internal/capabilities/scripts/jobs/generation_job_test.go.
func validateScriptGenerateWiring(root *ComposeRoot, log *zap.Logger) error {
	// (a) Registry has the type. Direct query against the canonical
	//     composition-time registry. The registry is frozen after
	//     Compose(); this query is branch-free.
	reg := appjobs.Compose()
	if !reg.IsRegistered(scriptpkg.TypeGenerate) {
		return fmt.Errorf("script.generate wiring (a): registry has no entry for %s; rebuild appjobs.Compose()", scriptpkg.TypeGenerate)
	}

	// (b) Broker has the handler. The RegisterJobs success above is
	//     the primary proof; this explicit re-check via the canonical
	//     broker query Service.HasHandler is the defence-in-depth
	//     invariant for the composition root. If a future refactor
	//     decouples RegisterJobs from the call site (or reorders the
	//     two calls), this check still surfaces the "no handler for
	//     script.generate" regression.
	if root == nil || root.Jobs == nil || root.Jobs.Service == nil {
		return fmt.Errorf("script.generate wiring (b): Jobs service is nil; the gate above should have tripped")
	}
	if !root.Jobs.Service.HasHandler(scriptpkg.TypeGenerate) {
		return fmt.Errorf("script.generate wiring (b): broker has no handler for %s; RegisterJobs call above should have registered it", scriptpkg.TypeGenerate)
	}

	// (c) At least one worker in the cluster is configured to claim
	//     script.generate. Forward-looking: when JobsBundle
	//     exposes a WorkerTypes field, uncomment the check below.
	//     Until then, the operator must rely on Worker.ExportTypes
	//     runtime audit.
	if log != nil {
		log.Info("validateScriptGenerateWiring: WorkerTypes not exposed yet; (c) check skipped (forward-looking)",
			zap.String("job_type", scriptpkg.TypeGenerate))
	}
	if log != nil {
		log.Info("validateScriptGenerateWiring: script.generate wiring complete",
			zap.String("job_type", scriptpkg.TypeGenerate))
	}
	return nil
}

// ── Postprocessor clip-search adapter structs (composition-root-local) ──

// artlistClipSearchAdapter wraps usecase.SearchArtlistClips into the
// adapters.ArtlistClipSearcher port. This adapter lives in the
// composition root (NOT in the adapters package) to avoid a circular
// import: adapters cannot import usecase.
//
// godlike/06 SSOT one-canonical-owner-per-fact: this is the canonical
// SOLE adapter between ArtlistClipSearcher and SearchArtlistClips.
type artlistClipSearchAdapter struct {
	svc          usecase.ClipServices
	remoteSearch func(context.Context, providerassets.SearchRequest) (providerassets.SearchResult, error)
}

// sqliteRealtimeSearchAdapter exposes the canonical SQLite clip catalog to
// script Artlist phrase search. The removed realtime package must not leave
// this capability as a silent successful empty result.
type sqliteRealtimeSearchAdapter struct {
	repo *sqassets.ClipsRepository
}

func (a *sqliteRealtimeSearchAdapter) SearchClips(ctx context.Context, query, source, _ string, _ int, _ float64) ([]usecase.RealtimeMatchAsset, error) {
	if a == nil || a.repo == nil {
		return nil, fmt.Errorf("sqlite realtime search: clip repository not configured")
	}
	if strings.TrimSpace(source) == "" {
		source = "artlist"
	}
	clips, err := a.repo.SearchClips(ctx, source, query)
	if err != nil {
		return nil, err
	}
	results := make([]usecase.RealtimeMatchAsset, 0, len(clips))
	for _, clip := range clips {
		if clip == nil {
			continue
		}
		score := clip.QualityScore()
		if score <= 0 {
			score = 1
		}
		results = append(results, usecase.RealtimeMatchAsset{
			ID:        clip.ID,
			Name:      clip.Name,
			Source:    string(clip.Source),
			Score:     score,
			DriveLink: clip.DriveLink(),
		})
	}
	return results, nil
}

var _ usecase.RealtimeSearchService = (*sqliteRealtimeSearchAdapter)(nil)

// SearchClips satisfies adapters.ArtlistClipSearcher.
func (a *artlistClipSearchAdapter) SearchClips(ctx context.Context, title string, phrases []string) ([]adapters.ArtlistClipMatch, error) {
	if a == nil {
		return nil, fmt.Errorf("ArtlistClipSearcher not configured")
	}
	if a.remoteSearch != nil {
		matches := make([]adapters.ArtlistClipMatch, 0, len(phrases))
		for _, phrase := range phrases {
			phrase = strings.TrimSpace(phrase)
			if phrase == "" {
				continue
			}
			result, err := a.remoteSearch(ctx, providerassets.SearchRequest{Query: phrase, Limit: 10})
			if err != nil {
				return nil, fmt.Errorf("artlist query %q: %w", phrase, err)
			}
			match := adapters.ArtlistClipMatch{Phrase: phrase, Remote: true}
			for _, candidate := range result.Assets {
				link := strings.TrimSpace(candidate.SourceRef)
				if link == "" {
					link = strings.TrimSpace(candidate.PreviewURL)
				}
				if link == "" {
					continue
				}
				match.ClipNames = append(match.ClipNames, candidate.Title)
				match.ClipDriveLinks = append(match.ClipDriveLinks, link)
				if match.FolderLink == "" {
					match.FolderLink = candidate.PageURL
				}
			}
			if len(match.ClipDriveLinks) > 0 {
				matches = append(matches, match)
			}
		}
		return matches, nil
	}
	suggestions := usecase.SearchArtlistClips(ctx, a.svc, title, phrases)
	if len(suggestions) == 0 {
		return nil, nil
	}

	// Convert usecase.ScriptArtlistClipSuggestion → adapters.ArtlistClipMatch.
	// adapters cannot import usecase types directly, so we convert at
	// the composition-root boundary.
	matches := make([]adapters.ArtlistClipMatch, 0, len(suggestions))
	for _, s := range suggestions {
		m := adapters.ArtlistClipMatch{
			Phrase:           s.Phrase,
			FolderLink:       s.FolderLink,
			FolderName:       s.FolderName,
			FolderID:         s.FolderID,
			TranslationError: s.TranslationError,
		}
		for _, c := range s.Clips {
			m.ClipNames = append(m.ClipNames, c.Name)
			m.ClipDriveLinks = append(m.ClipDriveLinks, c.DriveLink)
		}
		matches = append(matches, m)
	}
	return matches, nil
}

var _ adapters.ArtlistClipSearcher = (*artlistClipSearchAdapter)(nil)

// internetImageSearchAdapter wraps the canonical ImageSearchResolver
// into the adapters.InternetImageSearcher port.
type internetImageSearchAdapter struct {
	resolver imagesrouting.ImageSearchResolver
	log      *zap.Logger
}

func newInternetImageSearchAdapter(resolver imagesrouting.ImageSearchResolver, log *zap.Logger) *internetImageSearchAdapter {
	if log == nil {
		log = zap.NewNop()
	}
	return &internetImageSearchAdapter{resolver: resolver, log: log}
}

func (a *internetImageSearchAdapter) SearchImages(ctx context.Context, req adapters.InternetImageSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	if a == nil || a.resolver == nil {
		return nil, fmt.Errorf("internetImageSearchAdapter: resolver not wired")
	}
	var searcher imagesrouting.ImageSearcher
	var err error
	toCandidate := func(r imagesrouting.ImageSearchResult) scriptpkg.SegmentAssetCandidate {
		assetID := strings.TrimSpace(r.AssetID)
		if assetID == "" {
			assetID = strings.TrimSpace(r.Name)
		}
		if assetID == "" {
			sum := digest.SHA256Bytes([]byte(req.SegmentID + "\x00" + req.Query + "\x00" + r.PreviewURL))
			assetID = sum
		}
		return scriptpkg.SegmentAssetCandidate{
			AssetID: assetID, Provider: "internet_images", Query: strings.TrimSpace(req.Query),
			Entity: strings.TrimSpace(req.Entity), SourceURL: strings.TrimSpace(r.PreviewURL),
			SourcePageURL: strings.TrimSpace(r.SourcePageURL), PreviewURL: strings.TrimSpace(r.PreviewURL),
			DriveLink: strings.TrimSpace(r.DriveLink), LegacyFileMD5: strings.TrimSpace(r.LegacyFileMD5),
			Score: r.Score, Width: r.Width, Height: r.Height,
			RightsStatus: retrievedImageRightsStatus(r.License), RightsBasis: retrievedImageRightsBasis(r.License, r.Author),
		}
	}
	// Reuse durable retrieved images first. The optional resolver seam keeps
	// this DB-first policy out of the provider implementation; a fresh
	// randomized order avoids selecting the same stored image every run.
	if lookup, ok := a.resolver.(interface {
		ExistingImages(context.Context, string, int) ([]imagesrouting.ImageSearchResult, error)
	}); ok {
		cached, lookupErr := lookup.ExistingImages(ctx, req.Query, req.Limit)
		if lookupErr != nil {
			a.log.Warn("VidRush existing image lookup failed; falling back to DuckDuckGo", zap.String("query", req.Query), zap.Error(lookupErr))
		} else if len(cached) > 0 {
			rng := rand.New(rand.NewSource(time.Now().UnixNano()))
			rng.Shuffle(len(cached), func(i, j int) { cached[i], cached[j] = cached[j], cached[i] })
			out := make([]scriptpkg.SegmentAssetCandidate, 0, len(cached))
			for _, row := range cached {
				if strings.TrimSpace(row.DriveLink) == "" || strings.TrimSpace(row.LegacyFileMD5) == "" {
					continue
				}
				candidate := toCandidate(row)
				candidate.AcquisitionStatus = scriptpkg.VidRushStatusAcquired
				candidate.VerificationStatus = scriptpkg.VidRushStatusVerified
				candidate.PersistenceStatus = scriptpkg.VidRushStatusPersisted
				candidate.IndexStatus = "pending"
				out = append(out, candidate)
			}
			if len(out) > 0 {
				a.log.Info("VidRush reused persisted image candidates", zap.String("query", req.Query), zap.Int("count", len(out)))
				return out, nil
			}
		}
	}
	// VidRush internet_images is explicitly backed by DuckDuckGo. Resolve it
	// through the same routing/registry seam so provider selection remains
	// centralized and candidate provenance is preserved downstream.
	if explicit, ok := a.resolver.(interface {
		ResolveProvider(string) (imagesrouting.ImageSearcher, error)
	}); ok {
		searcher, err = explicit.ResolveProvider("duckduckgo")
	} else {
		searcher, err = a.resolver.Resolve(imagesrouting.TerritoryRetrieved)
	}
	if err != nil {
		return nil, err
	}
	filter := imagesrouting.ImageFilter{
		SubjectID: strings.TrimSpace(req.Query),
		Limit:     req.Limit,
	}
	// Every provider query gets a hard deadline. The common postprocessor
	// deadline is intentionally longer because it also covers acquisition,
	// verification and finalization; an unreachable retrieval backend must
	// never consume that entire budget.
	queryCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	results, err := searcher.Search(queryCtx, filter)
	if err != nil {
		a.log.Warn("VidRush internet image search failed", zap.String("query", req.Query), zap.Error(err))
		return nil, err
	}
	// Keep provider diagnostics at the VidRush boundary. The downstream
	// materializer deliberately drops candidates that cannot be acquired,
	// verified and persisted, so without this count an empty final binding
	// cannot distinguish an empty search from an acquisition rejection.
	if len(results) == 0 {
		a.log.Info("VidRush internet image search returned no candidates", zap.String("query", req.Query))
		return nil, nil
	}
	a.log.Info("VidRush internet image search returned candidates", zap.String("query", req.Query), zap.Int("count", len(results)))
	out := make([]scriptpkg.SegmentAssetCandidate, 0, len(results))
	for _, r := range results {
		out = append(out, toCandidate(r))
	}
	return out, nil
}

func retrievedImageRightsStatus(license string) string {
	license = strings.TrimSpace(license)
	if license != "" && !strings.EqualFold(license, "unknown") && !strings.EqualFold(license, "unverified") {
		return "verified"
	}
	return "unknown_allowed"
}

func retrievedImageRightsBasis(license, author string) string {
	license = strings.TrimSpace(license)
	author = strings.TrimSpace(author)
	if license == "" || strings.EqualFold(license, "unknown") || strings.EqualFold(license, "unverified") {
		return "source-license metadata required"
	}
	if author == "" {
		return "source license metadata: " + license
	}
	return "source license metadata: " + license + "; author: " + author
}

var _ adapters.InternetImageSearcher = (*internetImageSearchAdapter)(nil)

// ── ClipServices adapter structs (composition-root-local) ─────────────────

// driveCheckServiceAdapter wraps drive.Uploader.FileIsNotTrashed into
// usecase.DriveCheckService. This adapter lives in the composition
// root (NOT in the adapters or usecase packages) to avoid import cycles.
//
// godlike/06 SSOT: this is the canonical SOLE adapter between the
// drive.Uploader and the usecase.DriveCheckService port.
type driveCheckServiceAdapter struct {
	up interface {
		FileIsNotTrashed(ctx context.Context, fileID string) (bool, error)
	}
}

// FileIsNotTrashed satisfies usecase.DriveCheckService.
func (a *driveCheckServiceAdapter) FileIsNotTrashed(ctx context.Context, fileID string) (bool, error) {
	if a == nil || a.up == nil {
		return false, fmt.Errorf("driveCheckServiceAdapter: drive uploader not wired")
	}
	return a.up.FileIsNotTrashed(ctx, fileID)
}

var _ usecase.DriveCheckService = (*driveCheckServiceAdapter)(nil)

// jobsEnqueueServiceAdapter wraps appjobs.Service.Enqueue into
// usecase.JobEnqueueService. This adapter lives in the composition
// root (NOT in the adapters or usecase packages) to avoid import cycles.
//
// The adapter bridges the typed appjobs.Service.Enqueue(ctx,
// *job.EnqueueRequest) (*job.Job, error) to the interface-based
// usecase.JobEnqueueService.Enqueue(ctx, any) (any, error)
// expected by the artlist background job enqueue path.
//
// godlike/06 SSOT: this is the canonical SOLE adapter between the
// jobs.Service and the usecase.JobEnqueueService port.
type jobsEnqueueServiceAdapter struct {
	svc interface {
		Enqueue(ctx context.Context, req *job.EnqueueRequest) (*job.Job, error)
	}
}

// Enqueue satisfies usecase.JobEnqueueService.
func (a *jobsEnqueueServiceAdapter) Enqueue(ctx context.Context, req any) (any, error) {
	if a == nil || a.svc == nil {
		return nil, fmt.Errorf("jobsEnqueueServiceAdapter: jobs service not wired")
	}
	typedReq, ok := req.(*job.EnqueueRequest)
	if !ok {
		return nil, fmt.Errorf("jobsEnqueueServiceAdapter: req is %T, want *job.EnqueueRequest", req)
	}
	return a.svc.Enqueue(ctx, typedReq)
}

var _ usecase.JobEnqueueService = (*jobsEnqueueServiceAdapter)(nil)

// ── Composition validation: required processors MUST register ────────

// validateRequiredProcessors checks the post-freeze registry for
// every required processor name. Composition fails-closed: if any
// required name is missing, returns a typed error so the operator
// sees a clear restart-required message instead of silent runtime
// panics on the first plan that requested the missing processor.
//
// Returns a *scriptpkg.PlanInvalidError when one or more required
// processors are missing from the registry. Caller is the
// composition root, which wraps this with a context string.
//
// PR 2 (June 2026): gate that closes the "non-canonical WriteScript
// to dragnet" gap left by the previous partial-registration pattern
// (where composition would silently skip a Register call when the
// underlying dep was nil, then runtime would silently skip the
// postprocessor — leaving the script row unwritten).
func validateRequiredProcessors(ppReg *adapters.PostProcessorRegistry) *scriptpkg.PlanInvalidError {
	if ppReg == nil {
		return &scriptpkg.PlanInvalidError{
			ItemID:  "wireScriptFlow",
			Details: []string{"preflight: postprocessor registry is nil"},
		}
	}
	if !ppReg.IsFrozen() {
		return &scriptpkg.PlanInvalidError{
			ItemID:  "wireScriptFlow",
			Details: []string{"preflight: postprocessor registry must be frozen before required-processors validation"},
		}
	}
	required := adapters.RequiredProcessorNames()
	var missing []string
	for _, name := range required {
		if !ppReg.Registered(name) {
			missing = append(missing, string(name))
		} else if ppReg.LookupPolicy(name) != adapters.ProcessorRequired {
			// Defensive: composition-side invariant. A name in the
			// required list MUST have the ProcessorRequired
			// classification. If a future PR flips a processor's
			// policy to BestEffort, this check surfaces the
			// dependency drift loudly.
			missing = append(missing, string(name)+" (registered with non-required policy)")
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return &scriptpkg.PlanInvalidError{
		ItemID:  "wireScriptFlow",
		Details: []string{"preflight: required postprocessor(s) not registered at composition: " + strings.Join(missing, ", ")},
	}
}
