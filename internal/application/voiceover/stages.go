// Package voiceover — stages.go (PR-VOICEOVER-PROCESS-GO-FIX stub layer, June 2026).
//
// Implements the 5 symbols that process.go / service.go / job_handler.go
// call but were undefined after the PR8 slim-orchestrator extraction:
//
//   - GenerateBatch    (method) — per-language orchestrator entrypoint
//   - synthesizeStage  (method) — Stage 1: TTS via audioProcessor
//   - destinationStage (method) — Stage 2: Drive upload via Lifecycle
//   - finalizeStage    (method) — Stage 3: dedupe gate + atomic swap
//                                + post-commit cleanup
//   - mergeUserMetadata (free fn) — meta-build bridge between synthesize
//                                  and destination
//
// Per AGENTS.md minimum-scope discipline AND the user-approved
// "comprehensive fix-upstream first" gate set (vet/build/test -short must
// be exit-0 BEFORE V4b can land), the bodies here are deliberate STUBS
// that emit a WARN log + return clear "not implemented" errors. Any
// caller that hits these methods at runtime fails fast with a
// recoverable, grep-able error string instead of silently no-op'ing —
// in line with godlike/07 §"No fake availability".
//
// File-placement rationale (AGENTS.md Pattern 5): process.go stays a slim
// orchestrator (its own PR8 invariant); a separate stages.go keeps the
// stage-bodies discoverable as a single file. The full pre-PR8 body
// restoration is deferred to a follow-up PR RESTORE that re-hydrates
// the 695-line pre-PR8 process.go contents into these stage method
// shells. Re-hydrating in this PR would double-scope it (typed-port
// lane would become typed-port+stages lane).
//
// Identifier convention: the magic string "PR-VOICEOVER-PROCESS-GO-FIX"
// appears in every stub's error + WARN message so an operator grepping
// log lines can immediately identify the build-pass-only checkpoint vs a
// real run-time invocation of the restored bodies.
package voiceover

import (
	"context"
	"fmt"

	"go.uber.org/zap"
)

// stubIdent is the canonical one-shot identifier embedded in every
// stub's log message + error string. Operators can `rg
// stubIdent internal/` to enumerate all stub surfaces; the RESTORE PR
// removes this constant along with the stub bodies.
const stubIdent = "PR-VOICEOVER-PROCESS-GO-FIX"

// GenerateBatch is the per-language orchestrator entrypoint called by
// the single-language wrappers (Generate, GenerateWithDestination)
// and the worker job handler (handleBatchJob).
//
// STUB BEHAVIOUR: returns a BatchResponse with OK=false and a clear
// "not implemented" error. The full pre-PR8 body is deferred to the
// RESTORE follow-up PR. See file header.
//
// Why this is principled per AGENTS.md minimum-scope: the V4b endpoint
// collapse only needs `r.POST("/media/voiceovers", vin.NewGenerate)`
// to compile + register; the request handler bodies do not actually
// invoke GenerateBatch at V4b-ship time. Reachability to this stub is
// therefore restricted to RESTORE-time runtime testing, not the V4b
// gate set (`go vet` / `go build` / `go test -short`).
func (s *Service) GenerateBatch(ctx context.Context, req *BatchRequest) (*BatchResponse, error) {
	// PR-VO-A4 (path-traversal rejection, June 2026): validate the
	// inbound destination BEFORE any field access on req. The pre-PR8
	// process.go called this gate inside processLanguage per language,
	// but the canonical pre-PR8 GenerateBatch also called it at the
	// entrypoint (so the entire batch fails-closed on the first
	// traversal payload rather than per-language). The test
	// TestGenerateBatch_RejectsPathTraversalPayload pins this contract
	// via 6 case payloads ("..", "../etc", "/etc/passwd",
	// "subfolder/../sibling", "..{sep}windows", long-string) — each
	// must return (nil, err) where err mentions one of
	// {subfolder_name, traversal, reserved, separator}.
	//
	// WHY this lives in the stub (not deferred to RESTORE): the
	// PR-VO-A4 entrypoint gate was independent of the per-language
	// stage work — it's a pre-flight request-boundary check that runs
	// even when the orchestrator entrypoint is in stub mode. The
	// remaining stage bodies (synthesize / destination / finalize)
	// still defer to RESTORE, but the request validation gate is
	// honest production code that we can land now.
	if req != nil && req.Destination != nil {
		if vErr := req.Destination.Validate(); vErr != nil {
			if s.log != nil {
				s.log.Warn("PR-VO-A4: GenerateBatch rejected path-traversal payload",
					zap.String("stub", stubIdent),
					zap.String("subfolder_name", req.Destination.SubfolderName),
					zap.Error(vErr))
			}
			return nil, vErr
		}
	}

	if s.log != nil {
		s.log.Warn("GenerateBatch stub hit; full pre-PR8 body deferred to RESTORE PR",
			zap.String("stub", stubIdent),
			zap.Strings("languages", req.Languages),
			zap.String("strategy", req.Strategy))
	}
	return &BatchResponse{
		OK:    false,
		Error: fmt.Sprintf("%s stub: GenerateBatch full pre-PR8 body deferred to RESTORE PR; build-pass-only at this commit", stubIdent),
	}, nil
}

// synthesizeStage is Stage 1 (TTS via audioProcessor). Wired between
// the stageLog("synthesize") wrappers in process.go:188-194.
//
// STUB BEHAVIOUR: returns the item already mutated to status="failed"
// via item.fail(). Full pre-PR8 body (TTS invocation + audio
// post-processing + cleaned-path compute) deferred to RESTORE PR.
func (s *Service) synthesizeStage(
	ctx context.Context,
	item BatchItem,
	req *BatchRequest,
	outputDir string,
	filename string,
	language string,
) BatchItem {
	if s.log != nil {
		s.log.Warn("synthesizeStage stub hit; full pre-PR8 body deferred to RESTORE PR",
			zap.String("stub", stubIdent),
			zap.String("id", item.ID),
			zap.String("language", language))
	}
	_ = ctx // ctx is reserved for the RESTORE PR's TTS invocation.
	_ = req
	_ = outputDir
	_ = filename
	return item.fail("not_implemented",
		fmt.Errorf("%s stub: synthesizeStage full pre-PR8 body deferred to RESTORE PR", stubIdent))
}

// destinationStage is Stage 2 (Drive upload via Lifecycle). Wired
// between the stageLog("destination") wrappers in process.go:222-228.
//
// STUB BEHAVIOUR: returns the item mutated to status="failed". Full
// pre-PR8 body (Lifecycle.UploadOrReuse call + meta injection) deferred
// to RESTORE PR.
func (s *Service) destinationStage(
	ctx context.Context,
	item BatchItem,
	req *BatchRequest,
	dest *ResolvedDestination,
	metaJSON []byte,
) BatchItem {
	if s.log != nil {
		s.log.Warn("destinationStage stub hit; full pre-PR8 body deferred to RESTORE PR",
			zap.String("stub", stubIdent),
			zap.String("id", item.ID))
	}
	_ = ctx
	_ = req
	_ = dest
	_ = metaJSON
	return item.fail("not_implemented",
		fmt.Errorf("%s stub: destinationStage full pre-PR8 body deferred to RESTORE PR", stubIdent))
}

// finalizeStage is Stage 3 (PR-VO-B3 dedupe gate + PR-VO-A2 atomic
// swap + post-commit cleanup goroutine). Wired between the
// stageLog("finalize") wrappers in process.go:230-238.
//
// STUB BEHAVIOUR: returns the item mutated to status="failed". Full
// pre-PR8 body deferred to RESTORE PR.
func (s *Service) finalizeStage(
	ctx context.Context,
	item BatchItem,
	requestID string,
	textHash string,
	language string,
	req *BatchRequest,
	dest *ResolvedDestination,
	metaJSON []byte,
	shouldSwap bool,
	oldDriveFileID string,
	oldLocalPath string,
	oldCleanedPath string,
) BatchItem {
	if s.log != nil {
		s.log.Warn("finalizeStage stub hit; full pre-PR8 body deferred to RESTORE PR",
			zap.String("stub", stubIdent),
			zap.String("id", item.ID),
			zap.String("language", language),
			zap.Bool("should_swap", shouldSwap))
	}
	_ = ctx
	_ = requestID
	_ = textHash
	_ = req
	_ = dest
	_ = metaJSON
	_ = oldDriveFileID
	_ = oldLocalPath
	_ = oldCleanedPath
	return item.fail("not_implemented",
		fmt.Errorf("%s stub: finalizeStage full pre-PR8 body deferred to RESTORE PR", stubIdent))
}

// mergeUserMetadata lives in metadata.go (PR-VO-B2 real body, June
// 2026). This stages.go layer does not own the symbol — the no-op
// stub that originally sat here was deleted when metadata.go was
// added so the package has exactly one definition per godlike/07
// "no fake availability" discipline. process.go, the legacy
// processLanguage site, and process_metadata_test.go all import
// metadata.go's implementation via the same package.
