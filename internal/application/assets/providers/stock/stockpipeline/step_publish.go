// Package stockpipeline — step_publish.go
// (PR-STOCK-ORCHESTRATOR-SPLIT, July 2026).
//
// SOLE owner of StockPublishStep — the canonical implementation
// of the stock.publish step (Step 5 of the 6-step pipeline) per
// godlike/06 SSOT. §12-7 replaced the §12-5 Begin/Complete stub
// with the real AssetPreparation ladder:
//
//  1. For each composed chunk: ComputeAndFillSHA256 → Build
//     VerifiedArtifact (ArtifactID = stock:<fp>:chunk:<i>,
//     Required:true) → ArtifactPreparation.Prepare → translate
//     PublishedArtifact → ChunkState (RemoteFileID =
//     Location.FileID per godlike/06 FileID=location NOT
//     identity).
//
//  2. Build metadata.json envelopes:
//     - explicit `clips[]` requests publish one timestamp metadata
//     JSON per requested clip directory
//     - legacy runs keep the single per-run metadata.json envelope
//     Each envelope is written to temp, hashed, then sent through
//     ArtifactPreparation.Prepare → translate → MetadataState.
//
// godlike/07 fail-closed contracts:
//   - AssetPreparation nil → State.Published = nil, return nil
//     (test-fixture compat). Downstream stock.finalize's
//     BuildFinalizationRequest will raise
//     ErrStockNoChunksFinalized.
//   - Prepare returns error → abort with
//     ErrStockPublishArtifactFailed (wraps publisher fault;
//     preserves typed sentinel via %w+errors.Is).
//   - ComputeAndFillSHA256 returns error → abort (ChunkState
//     sentinel propagates verbatim — VerifyChunks surfaces
//     ErrStockChunkHashMissing / ErrStockChunkLocalMissing
//     consistently).
package stockpipeline

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	"github.com/Marcuss-ops/PipelineGen/pkg/pathutil"
	"github.com/Marcuss-ops/PipelineGen/pkg/slug"
)

// StockPublishStep is the canonical implementation of
// stock.publish. §12-7 replaces the §12-5 Begin/Complete stub
// with the real AssetPreparation ladder.
type StockPublishStep struct{}

func (StockPublishStep) Name() string { return StepKeyStockPublish }

func (StockPublishStep) Run(ctx context.Context, runner StepRunner) error {
	if runner.ArtifactPreparation() == nil {
		// Test-fixture path: no AssetPreparation wired → no chunks
		// prepared. StockFinalizeStep's BuildFinalizationRequest gate
		// raises ErrStockNoChunksFinalized — that's the intended
		// fail-closed signal for unwired composition roots + tests.
		if runner.Log() != nil {
			runner.Log().Debug("orchestrator: stock.publish: ArtifactPreparation nil — skipping upload (test-fixture path)")
		}
		runner.State().Published = nil
		return nil
	}

	if runner.Log() != nil {
		runner.Log().Info("orchestrator: stock.publish: starting",
			zap.Int("composed_paths", len(runner.State().ComposedPaths)))
		runner.Log().Info("orchestrator: stock.publish: AssetPreparation wired — preparing chunks + metadata")
	}

	in := runner.RunInput()
	fp := runner.RunFingerprint()
	explicitTimestamps := in != nil && len(in.Clips) > 0
	rootFolderName := stockRootFolderName(in)
	timestampGroupName := stockTimestampGroupName(in)
	composed := runner.State().ComposedPaths
	chunks := make([]ChunkState, 0, len(composed))
	metadataArtifactID := MetadataArtifactID(fp)
	var metadataPublished finalization.PublishedArtifact

	// ── Phase 1: per-chunk ArtifactPreparation ─────────────────────
	// PR-003 (July 2026): per-run total chunk count = the
	// post-plan length of runner.State().Plan. Repeated per-entry
	// per user spec (logically a run-level scalar — see
	// ChunkState.TotalChunks godoc + ChunkMetadataEntry field
	// godoc for the duplication rationale). Pre-compute once
	// outside the loop per godlike/07 minimum-blast-radius.
	totalChunks := len(runner.State().Plan)
	// PR-004 (July 2026): pre-compute policyVersion once per run
	// so the per-chunk assignment is byte-equivalent across the
	// whole run (locks traceability + avoids N-times strings.TrimSpace
	// in the hot loop). Fallback to StockTimestampPolicyVersionV1
	// when RunInput.PolicyVersion is empty/whitespace per user
	// spec. Stamped on every chunk via ChunkState.PolicyVersion.
	policyVersion := StockTimestampPolicyVersionV1
	if in != nil {
		if trimmed := strings.TrimSpace(in.PolicyVersion); trimmed != "" {
			policyVersion = trimmed
		}
	}
	for i, compPath := range composed {
		plan := ClipPlan{}
		hasPlan := i < len(runner.State().Plan)
		if hasPlan {
			plan = runner.State().Plan[i]
		}
		cs := ChunkState{
			Index:         i,
			ArtifactID:    ChunkArtifactID(fp, i),
			Filename:      ChunkArtifactFilename(fp, i),
			LocalPath:     compPath,
			TotalChunks:   totalChunks,
			PolicyVersion: policyVersion,
		}
		if hasPlan {
			cs.SourceURL = plan.SourceID
			cs.SourceProvider = plan.SourceProvider
			cs.SourceVideoID = plan.SourceVideoID
			cs.StartSec = plan.StartSec
			cs.EndSec = plan.EndSec
			cs.Description = plan.Description
			// PR-STOCK-TIMESTAMP-CLIPS Front 2 (July 2026): thread the
			// 4 new content fields from ClipPlan → ChunkState. Tags
			// gets a defensive copy so downstream mutation (rare but
			// possible for retry paths that reuse ChunkState) doesn't
			// leak into the plan.
			cs.Round = plan.Round
			cs.Tags = append([]string(nil), plan.Tags...)
			cs.Category = plan.Category
			cs.Slug = plan.Slug
		}
		if explicitTimestamps {
			cs.ArtifactID = TimestampArtifactID(fp, i, "video")
			if i < len(runner.State().Plan) && runner.State().Plan[i].Title != "" {
				cs.Title = runner.State().Plan[i].Title
			} else if i < len(in.Clips) {
				cs.Title = in.Clips[i].Title
			}
			// PR-STOCK-TIMESTAMP-CLIPS Front 3 (July 2026): sync
			// plan.Title to the resolved cs.Title so perClipLeafName
			// uses the SAME source-of-truth title the chunk is
			// indexed/displayed by. Without this, an empty Plan.Title
			// + populated in.Clips[i].Title would land the chunk in a
			// start-end-named subdir while the chunk title is "Round 7"
			// (godlike/07 NO-FAKE-AVAILABILITY: avoid the title/leaf
			// mismatch that confuses operators scanning Drive).
			if cs.Title != "" {
				plan.Title = cs.Title
			}
		}
		if compPath != "" {
			if err := cs.ComputeAndFillSHA256(); err != nil {
				// P6 (July 2026): compose_chunks now produces real
				// files — ErrStockChunkLocalMissing is a hard failure.
				return fmt.Errorf("orchestrator: stock.publish: chunk %d (artifact=%s): %w",
					i, cs.ArtifactID, err)
			}
		}
		idem, idemErr := asset.SHA256IdempotencyKey("stock", cs.SHA256)
		if idemErr != nil {
			return fmt.Errorf("%w: chunk %d (artifact=%s) idem-key: %v",
				ErrStockPublishArtifactFailed, i, cs.ArtifactID, idemErr)
		}
		// PR-STOCK-TIMESTAMP-CLIPS Front 3 (July 2026): per-clip
		// PathLeafName for explicit-clips runs (each clip lands in
		// its own Drive subdir, e.g. round-1/, round-7-broner-barcolla/).
		// Legacy (no clips[]) stays on the shared timestampGroupName
		// to preserve the TestStockPublishStep_LegacyMultipleChunks_SharedPathLeafName
		// invariant — godlike/07 minimum-blast-radius gates the new
		// behavior strictly on explicitTimestamps.
		var leafName string
		if explicitTimestamps {
			leafName = perClipLeafName(plan)
		} else {
			leafName = timestampGroupName
		}
		va := finalization.VerifiedArtifact{
			ArtifactID:     cs.ArtifactID,
			Kind:           finalization.KindVideo,
			Filename:       cs.Filename,
			MIMEType:       "video/mp4",
			LocalPath:      cs.LocalPath,
			SizeBytes:      cs.SizeBytes,
			SHA256:         cs.SHA256,
			Requirement:    finalization.ArtifactRequirementRequired,
			IdempotencyKey: idem + ":c" + strconv.Itoa(i),
			Description:    cs.Description,
			// DRIVE-IS-DRIVE (July 2026): RootFolderOverride REMOVED.
			// Stock no longer passes FolderID as a Drive path override.
			// The artifact publisher adapter derives Group/Subject from
			// RootFolderName + PathLeafName via stockArtifactPathParts.
			// The DestinationRegistry + PathBuilder handle routing.
			RootFolderName: rootFolderName,
			PathLeafName:   leafName,
		}
		published, prepErr := runner.ArtifactPreparation().Prepare(ctx, va)
		if prepErr != nil {
			return fmt.Errorf("%w: chunk %d (artifact=%s): %v",
				ErrStockPublishArtifactFailed, i, cs.ArtifactID, prepErr)
		}
		cs.RemoteFileID = published.Location.FileID
		cs.RemoteWebViewLink = published.Location.WebViewLink
		// PR-004 (July 2026): capture the canonical Drive webview
		// link as DrivePath. The Qdrant semantic-payload enrichment
		// wave expects the wire-shape key drive_path on chunk rows;
		// the legacy metadata.json still uses drive_web_view_link
		// (preserved on RemoteWebViewLink above). Same source-of-truth
		// (PublishedArtifact.Location.WebViewLink) — the field-name
		// divergence is the canonical SSOT-vs-legacy-tradeoff the
		// user spec asks for (godlike/07 minimum-blast-radius: no
		// new surface contract, just a typed alias on the struct).
		cs.DrivePath = published.Location.WebViewLink
		cs.RemoteDownloadLink = published.Location.DownloadLink

		// PR-PER-CLIP-METADATA (July 2026, DoD 5): per-clip
		// metadata.json in the SAME per-clip subdir as the video
		// chunk. The per-clip envelope reuses buildStockRunMetadata
		// with a singleton chunks slice (same wire shape as the
		// run-level metadata.json, only cardinality differs) so
		// the canonical StockRunMetadata is the SOLE metadata
		// wire shape. Lands at PathLeafName = leafName (the SAME
		// subdir as the video). godlike/07 minimum-blast-radius:
		// the existing cs is reused (no extra ChunkState build)
		// so the video path stays byte-equivalent for callers
		// that don't care about the per-clip metadata.
		clipMetaPath, clipMetaHash, clipMetaSize, clipMetaErr := writeAndHashPerClipMetadata(in, cs, fp)
		if clipMetaErr != nil {
			// PR-PER-CLIP-METADATA fix (July 2026, code-reviewer item #1):
			// dual-%w preserves the underlying typed-error chain
			// (json.SyntaxError / os.PathError) so callers can probe
			// the root cause via errors.As — godlike/07 typed-error
			// contract. Single-%w with %v would drop the chain.
			return fmt.Errorf("%w: per-clip metadata.json stage for chunk %d (artifact=%s): %w",
				ErrStockPublishArtifactFailed, i, cs.ArtifactID, clipMetaErr)
		}
		defer func() {
			if rmErr := os.Remove(clipMetaPath); rmErr != nil && !os.IsNotExist(rmErr) {
				if runner.Log() != nil {
					runner.Log().Warn("orchestrator: stock.publish: failed to remove per-clip metadata temp file",
						zap.String("path", clipMetaPath), zap.Int("chunk_index", i), zap.Error(rmErr))
				}
			}
		}()

		clipMetaIdem, clipMetaIdemErr := asset.SHA256IdempotencyKey("stock:"+fp+":clip-metadata:"+strconv.Itoa(i), clipMetaHash)
		if clipMetaIdemErr != nil {
			// dual-%w: godlike/07 typed-error chain preserved.
			return fmt.Errorf("%w: per-clip metadata idem-key for chunk %d: %w",
				ErrStockPublishArtifactFailed, i, clipMetaIdemErr)
		}
		var clipMetaArtifactID string
		if explicitTimestamps {
			clipMetaArtifactID = TimestampArtifactID(fp, i, "metadata")
		} else {
			clipMetaArtifactID = ChunkArtifactID(fp, i) + ":metadata"
		}
		clipMetaVA := finalization.VerifiedArtifact{
			ArtifactID:     clipMetaArtifactID,
			Kind:           finalization.KindMetadata,
			Filename:       "metadata.json",
			MIMEType:       "application/json",
			LocalPath:      clipMetaPath,
			SizeBytes:      clipMetaSize,
			SHA256:         clipMetaHash,
			Requirement:    finalization.ArtifactRequirementRequired,
			IdempotencyKey: clipMetaIdem,
			// DRIVE-IS-DRIVE (July 2026): RootFolderOverride REMOVED.
			// Stock no longer passes FolderID as a Drive path override.
			// The artifact publisher adapter derives Group/Subject from
			// RootFolderName + PathLeafName via stockArtifactPathParts.
			RootFolderName: rootFolderName,
			// SAME subdir as the video chunk — per-clip metadata.json
			// lands inside the per-clip leaf folder (e.g. round-1/,
			// round-7-broner-barcolla/), alongside the video. Legacy
			// mode lands inside the shared timestampGroupName leaf
			// alongside the legacy shared-folder videos.
			PathLeafName: leafName,
		}
		if _, clipMetaPrepErr := runner.ArtifactPreparation().Prepare(ctx, clipMetaVA); clipMetaPrepErr != nil {
			// dual-%w: godlike/07 typed-error chain preserved.
			return fmt.Errorf("%w: per-clip metadata.json upload for chunk %d (artifact=%s): %w",
				ErrStockPublishArtifactFailed, i, clipMetaArtifactID, clipMetaPrepErr)
		}

		chunks = append(chunks, cs)
	}
	runner.State().Published = chunks

	// ── Phase 2: metadata.json ArtifactPreparation ────────────────
	// Timestamp-mode and legacy runs both publish a single
	// metadata.json envelope. The difference is only the leaf
	// folder name used for the publish path.
	//
	// STATO ATTUALE: compose_chunks produce file reali.
	// L'ErrStockChunkLocalMissing è RIMOSSO — chunk mancanti sono
	// hard failure.
	//
	// PROSSIMO STEP: rimuovere questo guard quando compose_chunks
	// è sempre wired in produzione. Oggi il renderer può essere nil
	// in test-fixture mode, quindi il guard è ancora necessario.
	if len(chunks) == 0 {
		// godlike/07 fail-closed (PR-STOCK-RESUME-STATE-LOSS, July 2026):
		// if AssetPreparation is wired (production mode) but ComposedPaths
		// was empty (zero chunks prepared), the runState was lost on resume
		// (or compose_chunks short-circuited). Returning nil here would be
		// a silent-success false-positive — the job would declare SUCCEEDED
		// without uploading anything. The leniency is preserved ONLY for
		// test-fixture mode (AssetPreparation nil) where empty chunks is
		// the expected outcome of a stub run.
		if runner.ArtifactPreparation() != nil {
			if runner.Log() != nil {
				runner.Log().Error("orchestrator: stock.publish: ArtifactPreparation wired but ComposedPaths empty — fail-closed on resume state-loss")
			}
			return ErrStockPublishStateLost
		}
		if runner.Log() != nil {
			runner.Log().Debug("orchestrator: stock.publish: zero chunks prepared — skipping metadata publication (pre-Commit-7 stub)")
		}
		return nil
	}
	metaPath, metaHash, metaSize, metaErr := writeAndHashMetadata(in, chunks, fp)
	if metaErr != nil {
		return fmt.Errorf("%w: metadata.json stage: %v",
			ErrStockPublishArtifactFailed, metaErr)
	}
	defer func() {
		if rmErr := os.Remove(metaPath); rmErr != nil && !os.IsNotExist(rmErr) {
			if runner.Log() != nil {
				runner.Log().Warn("orchestrator: stock.publish: failed to remove metadata temp file",
					zap.String("path", metaPath), zap.Error(rmErr))
			}
		}
	}()

	metaIdem, metaIdemErr := asset.SHA256IdempotencyKey("stock:"+fp+":metadata", metaHash)
	if metaIdemErr != nil {
		return fmt.Errorf("%w: metadata idem-key: %v",
			ErrStockPublishArtifactFailed, metaIdemErr)
	}
	// PR-STOCK-TIMESTAMP-CLIPS Front 3 (July 2026): metadata.json
	// sits at the run-root level in explicit-clips mode (the
	// canonical "metadata/" subdir alongside the per-clip video
	// subdirs like round-1/, round-2/). Legacy stays on the shared
	// timestampGroupName leaf (preserves the legacy
	// TestStockPublishStep_LegacyMultipleChunks_SharedPathLeafName
	// invariant for the metadata artifact).
	var metaLeafName string
	if explicitTimestamps {
		metaLeafName = "metadata"
	} else {
		metaLeafName = timestampGroupName
	}
	metaVA := finalization.VerifiedArtifact{
		ArtifactID:     MetadataArtifactID(fp),
		Kind:           finalization.KindMetadata,
		Filename:       "metadata.json",
		MIMEType:       "application/json",
		LocalPath:      metaPath,
		SizeBytes:      metaSize,
		SHA256:         metaHash,
		Requirement:    finalization.ArtifactRequirementRequired,
		IdempotencyKey: metaIdem,
		// DRIVE-IS-DRIVE (July 2026): RootFolderOverride REMOVED.
		// Stock no longer passes FolderID as a Drive path override.
		RootFolderName: rootFolderName,
		PathLeafName:   metaLeafName,
	}
	metaPublished, metaPrepErr := runner.ArtifactPreparation().Prepare(ctx, metaVA)
	if metaPrepErr != nil {
		return fmt.Errorf("%w: metadata.json upload: %v",
			ErrStockPublishArtifactFailed, metaPrepErr)
	}
	metadataPublished = metaPublished
	runner.State().MetadataPublished = MetadataState{
		LocalPath:         metaVA.LocalPath,
		SHA256:            metaVA.SHA256,
		SizeBytes:         metaVA.SizeBytes,
		RemoteFileID:      metaPublished.Location.FileID,
		RemoteWebViewLink: metaPublished.Location.WebViewLink,
	}

	if runner.Log() != nil {
		runner.Log().Info("orchestrator: stock.publish: SUCCEEDED",
			zap.Int("chunk_count", len(chunks)),
			zap.String("metadata_artifact_id", metadataArtifactID),
			zap.String("metadata_remote_file_id", metadataPublished.Location.FileID))
	}
	return nil
}

// stockRootFolderName derives the human-readable Drive top-level
// folder name for a stock pipeline run. Canonical SSOT for legacy
// "no-folder-name" legibility (July 2026). Source evaluation order:
//
//  1. in.FolderName — operator-supplied readable name (highest priority).
//  2. in.Subfolder   — operator-supplied secondary root.
//  3. in.SearchQueries[0] — sanitized first query (legacy search-only).
//  4. in.DirectURLs[0]   — basename of first URL (legacy direct-url).
//  5. "stock_<YYYY-MM-DD>" UTC date — universal fallback.
//
// Pre-change legibility gap: a legacy run that omits folder_name +
// clips[] surfaced either "stock" or run_<fingerprint> on Drive —
// both illegible to operators scanning the Drive folder tree. The
// fallback chain above preserves the first two branches
// byte-stable for operators who DO supply folder_name (or
// subfolder) and extends with three new fallback rules for the
// legacy path.
//
// godlike/06 SSOT: this function is the SOLE writer of stock
// pipeline root folder names. The VerifiedArtifact.RootFolderName
// field (locked in PR-STOCK-ORCHESTRATOR-SPLIT, July 2026) carries
// the chosen value into the artifact publisher adapter
// stockArtifactPathParts switch which honors it via
// stockRunFolderName.
//
// godlike/07 minimum-blast-radius: zero new signatures, zero new
// dependencies, zero composition-root surface change. Date fallback
// runs at request time (no cached time) so test fixtures and replay
// paths stay stable. The nil RunInput branch returns "stock"
// (preserving pre-change behavior for legacy fixture flows).
func stockRootFolderName(in *RunInput) string {
	if in == nil {
		return "stock"
	}
	if name := sanitizedRootName(in.FolderName); name != "" {
		return name
	}
	if name := sanitizedRootName(in.Subfolder); name != "" {
		return name
	}
	if name := sanitizeLegacyQuery(in.SearchQueries); name != "" {
		return name
	}
	if name := sanitizeLegacyURLBasename(in.DirectURLs); name != "" {
		return name
	}
	return "stock_" + time.Now().UTC().Format("2006-01-02")
}

// sanitizedRootName trims whitespace, returns "" for empty input,
// otherwise runs through pathutil.SafeFolderName. Empty-in / all-
// whitespace-in collapses to "" so the caller continues to the
// next fallback rule (closes the pre-existing latent bug where
// empty FolderName silently surfaced as "untitled" on Drive).
func sanitizedRootName(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	return pathutil.SafeFolderName(s)
}

// sanitizeLegacyQuery returns the first non-empty sanitized search
// query from queries. Returns "" if none survive so the caller
// falls through to the URL / date fallback rule.
func sanitizeLegacyQuery(queries []string) string {
	for _, q := range queries {
		if name := sanitizedRootName(q); name != "" {
			return name
		}
	}
	return ""
}

// sanitizeLegacyURLBasename returns the first non-empty sanitized
// URL basename (sans file extension) from urls. Returns "" if none
// survive so the caller falls through to the date fallback rule.
func sanitizeLegacyURLBasename(urls []string) string {
	for _, u := range urls {
		if name := sanitizedURLBasename(u); name != "" {
			return name
		}
	}
	return ""
}

// sanitizedURLBasename strips query + fragment layers from a raw
// URL, takes the URL path's Base, strips the file extension, then
// runs through sanitizedRootName so the result is a filesystem-
// safe folder name. Returns "" on empty / malformed input so
// callers fall through to the next fallback rule.
func sanitizedURLBasename(rawURL string) string {
	s := strings.TrimSpace(rawURL)
	if s == "" {
		return ""
	}
	if i := strings.IndexByte(s, '?'); i >= 0 {
		s = s[:i]
	}
	if i := strings.IndexByte(s, '#'); i >= 0 {
		s = s[:i]
	}
	if parsed, err := url.Parse(s); err == nil && parsed.Path != "" {
		s = parsed.Path
	}
	base := filepath.Base(s)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	return sanitizedRootName(base)
}

// stockTimestampGroupName derives the human-readable Drive leaf
// folder name shared by all chunks + the per-run metadata.json
// of a stock pipeline run. Cascade:
//
//  1. basename of in.Subfolder (operator-supplied path).
//  2. in.FolderName (operator-supplied readable name).
//  3. "metadata" (universal literal-name fallback).
//
// Empty in.FolderName (or all-whitespace) is no longer incorrectly
// surfaced as the literal "untitled" string. The pre-existing bug
// was exposed by TestStockPublishStep_LegacyMultipleChunks_SharedPathLeafName:
// when in.FolderName == "" AND in.Subfolder == "" the function
// returned pathutil.SafeFolderName("") = "untitled", which then
// surfaced on Drive as the literal folder name `untitled/`. The
// fix threads in.FolderName through sanitizedRootName which
// trims+empty-checks BEFORE SafeFolderName, returning "" for
// empty input so the caller falls through to the "metadata"
// literal fallback.
//
// godlike/06 SSOT: this is the SOLE writer of stock
// PathLeafName values. VerifiedArtifact.PathLeafName (locked in
// PR-STOCK-ORCHESTRATOR-SPLIT, July 2026) carries the chosen
// value into every chunk + metadata artifact in a run, so all
// chunks + metadata share the SAME leaf (no per-chunk drift).
// perClipLeafName derives the canonical per-clip Drive leaf folder
// name for explicit-clips (timestamp-mode) runs. Priority cascade
// (per user diagnostic "round-01-la-fase-di-studio" example):
//
//  1. slugify(plan.Title) — operator-supplied clip title (e.g.
//     "Round 7 - Broner barcolla" → "round-7-broner-barcolla").
//     The slug matches the canonical convention established by
//     pkg/stockparser (SafeFolderName → ToLower → space-to-hyphen
//     + collapse-consecutive-hyphens + trim-edges).
//  2. fmt.Sprintf("%02d-%02d-%02d_to_%02d-%02d-%02d", ...)
//     (StartSec, EndSec) — universal HH-MM-SS_to_HH-MM-SS literal
//     for the empty-Title / whitespace-Title / pathutil-empty
//     fallback case (preserves the "00-00-32_to_00-01-27" shape
//     the legacy code emitted for the run-level leaf).
//
// godlike/07 NO-FAKE-AVAILABILITY: the slug is NEVER "untitled"
// (pathutil.SafeFolderName's all-whitespace fallback). When the
// title produces an empty slug, the function falls through to
// the start-end literal — operators see a stable, parseable
// leaf instead of a generic "untitled" folder that shadows
// other runs.
//
// godlike/06 SSOT: this is the SOLE writer of per-clip
// PathLeafName values for explicit-clips runs. The legacy
// stockTimestampGroupName function stays as the SOLE writer
// of shared-leaves for legacy (no clips[]) runs and for the
// per-run metadata.json — the two functions are disjoint by
// gate (explicitTimestamps=true vs false) per godlike/07
// minimum-blast-radius.
//
// PR-STOCK-TIMESTAMP-CLIPS Front 3 (July 2026): replaces the
// pre-PR bug where every chunk in an explicit-clips run shared
// a single PathLeafName (= stockTimestampGroupName), so all 8
// Pacquiao/Broner clips landed in the same folder instead of
// in per-clip subdirs.
func perClipLeafName(plan ClipPlan) string {
	// PR-STOCK-TIMESTAMP-CLIPS Front 2 (July 2026): explicit-override
	// cascade. The user-supplied Slug (when non-empty) is the canonical
	// leaf — it wins over the title-derived slug. We still run it
	// through pathutil.SafeFolderName so the result is filesystem-safe
	// (no /, no :, no leading dot). godlike/07 NO-FAKE-AVAILABILITY:
	// the SafeFolderName all-whitespace fallback "untitled" is also
	// rejected. Critically, we ALSO reject slugs that sanitize to
	// pure-punctuation (e.g. "///" → "___", "!!!" → "___") because
	// those would shadow real folders on Drive without any
	// human-readable meaning. If the user-supplied slug is all
	// whitespace / unsafe / punctuation-only, fall through to the
	// title-derived cascade rather than emit a meaningless folder
	// (operators scanning Drive would see a generic ___/ folder
	// that shadows other runs).
	if raw := strings.TrimSpace(plan.Slug); raw != "" {
		if safe := pathutil.SafeFolderName(raw); safe != "" && safe != "untitled" && hasAlphanumeric(safe) {
			return safe
		}
	}
	if title := strings.TrimSpace(plan.Title); title != "" {
		slug := slugifyTitle(title)
		// godlike/07: never emit "untitled" as a slug (the
		// pathutil.SafeFolderName all-whitespace fallback).
		// Empty-after-slugify also falls through to the
		// start-end literal.
		if slug != "" && slug != "untitled" {
			return slug
		}
	}
	return fmt.Sprintf("%02d-%02d-%02d_to_%02d-%02d-%02d",
		int(plan.StartSec)/3600, (int(plan.StartSec)%3600)/60, int(plan.StartSec)%60,
		int(plan.EndSec)/3600, (int(plan.EndSec)%3600)/60, int(plan.EndSec)%60,
	)
}

// hasAlphanumeric returns true if s contains at least one letter or
// digit. Used by perClipLeafName to reject slugs that sanitize to
// pure-punctuation (e.g. "___", "---", "...") so the cascade falls
// through to the title-derived slug (godlike/07 NO-FAKE-AVAILABILITY:
// meaningless folder names shadow real ones on Drive).
func hasAlphanumeric(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

// slugifyTitle is a thin package-internal alias for the canonical
// pkg/slug.SlugifyTitle helper. Per godlike/06 SSOT, pkg/slug is
// the SOLE canonical owner of the title-slug convention; both
// the parser-side surface (pkg/stockparser/parser.go::deriveSlug)
// and this stock-pipeline surface route through it for
// byte-equivalent output (PR-SLUG-HELPER-EXTRACT, July 2026).
//
// The local alias is preserved (not deleted outright) so the
// perClipLeafName call site (above) stays grep-stable and the
// pre-extraction goddoc comment block documents the surface
// transition for future readers. Forward-pointer: a future
// refactor can inline-slugifyTitle to a direct call to
// pkg/slug.SlugifyTitle if the per-call overhead warrants it
// (today: the inliner is unrolled by the Go compiler — both
// paths have identical zero-cost).
func slugifyTitle(title string) string {
	return slug.SlugifyTitle(title)
}

func stockTimestampGroupName(in *RunInput) string {
	if in == nil {
		return "metadata"
	}
	if sub := strings.TrimSpace(in.Subfolder); sub != "" {
		if base := filepath.Base(filepath.Clean(sub)); base != "" && base != "." && base != string(filepath.Separator) {
			if name := sanitizedRootName(base); name != "" {
				return name
			}
		}
	}
	if name := sanitizedRootName(in.FolderName); name != "" {
		return name
	}
	return "metadata"
}
