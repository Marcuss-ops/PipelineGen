package voiceover

// ────────────────────────────────────────────────────────────────────────
// PR-VO-AUDIT-P01 (June 2026): typed state-machine for voiceover.
//
// REPLACES the legacy string-literal status + ad-hoc failure codes with
// two distinct types (Status / FailureCode). Both underlying strings
// remain identical to the legacy wire shape so JSON consumers see no
// change. The compile-time typed comparison is the whole point: legacy
// checks `if item.Status == "failed"` silently missed every failure
// literal that wasn't exactly "failed" (tts_failed, upload_failed,
// missing_folder_id, etc.), allowing a TTS-failed item to reach the
// finalizeStage and commit a record with Status="completed".
//
// After the typed-enum refactor, ANY failure code flows through the
// canonical fail() helper which normalises to Status=StatusFailed;
// downstream aggregate check `if item.Status == StatusFailed` is
// exhaustive by construction — no substring matching, no "*/_failed"
// gap, no silent false-success.
//
// JSON wire compat: `type Status string` + `type FailureCode string`
// both serialise into the same byte-for-byte strings as the pre-P01
// literal fields. omitempty on Errors[] keeps happy-path rows compact.
// ────────────────────────────────────────────────────────────────────────

// Status is the per-item terminal/active state. Typed so the runtime
// aggregate check (Status == StatusFailed) is exhaustive at compile time
// and cannot silently miss any "*_failed" sub-state.
type Status string

func (s Status) String() string { return string(s) }

const (
	// StatusProcessing is the initial state set by process.go right
	// after ID + filename build. Visible to API consumers while a
	// pipeline is in-flight; finalizeStage closes with StatusCompleted
	// before commit.
	StatusProcessing Status = "processing"
	// StatusGenerated is the post-synthesize state. Stage 1 success.
	StatusGenerated Status = "generated"
	// StatusUploaded is the post-Lifecycle.ProcessAsset state.
	// Stage 2 success — Drive link + file ID populated.
	StatusUploaded Status = "uploaded"
	// StatusCompleted is the post-finalize state. Stage 3 success —
	// commit succeeded, row in SQLite + index event enqueued in
	// the outbox (atomic, single tx).
	StatusCompleted Status = "completed"
	// StatusFailed is the canonical aggregate failure state. ALL
	// failure codes (FailureCode consts below) normalise to this
	// Status via the BatchItem.fail helper, which preserves the
	// specific FailureCode in item.Errors so the forensic trail
	// survives without breaking the aggregate OK=false contract.
	StatusFailed Status = "failed"
)

// FailureCode is the structured per-failure-mode code. fail() appends
// the call's FailureCode to item.Errors so callers can correlate the
// canonical StatusFailed with the specific failure mode. Each constant
// maps 1-a-1 to the pre-P01 literal status string; the refactor replaces
// the literal with the typed constant without disturbing the JSON wire
// shape.
type FailureCode string

const (
	// FailureTTSProviderUnavailable — synthesizeStage: ttsProvider
	// is nil (composition root did not wire it).
	FailureTTSProviderUnavailable FailureCode = "tts_provider_unavailable"
	// FailureTTS — synthesizeStage: TTSProvider.Synthesize returned
	// an error (Python crash, edge-tts bridge failure, FFmpeg
	// post-process error, etc.).
	FailureTTS FailureCode = "tts_failed"
	// FailureAudioPost — audio post-processing failed after synthesis.
	FailureAudioPost FailureCode = "audio_post_process_failed"
	// FailureVoiceRegistry — the canonical language registry was
	// missing or did not authorize a usable TTS voice. This is a
	// permanent configuration failure; the bridge must not invent a
	// fallback voice.
	FailureVoiceRegistry FailureCode = "voice_registry_unavailable"
	// FailureLifecycleUnavailable — destinationStage: lifecycleService
	// is nil (composition root did not wire it).
	FailureLifecycleUnavailable FailureCode = "lifecycle_unavailable"
	// FailureMissingFolder — destinationStage: destination.FolderID
	// is empty (resolver short-circuit; canonical destination
	// resolver surfaces ErrMissingFolder at the resolve step too).
	FailureMissingFolder FailureCode = "missing_folder_id"
	// FailureNoLocalPayload — destinationStage: synthesizeStage
	// produced no local path (Stage 2 cannot upload).
	FailureNoLocalPayload FailureCode = "no_local_payload"
	// FailureUpload — destinationStage: lifecycleService.ProcessAsset
	// returned an error (Drive upload failed, dedupe gate hard-
	// rejected, etc.).
	FailureUpload FailureCode = "upload_failed"
	// FailureMetadataSerialization — publishStage could not serialize
	// the canonical metadata envelope. The pipeline must fail before
	// publishing so a Drive asset is never created without durable metadata.
	FailureMetadataSerialization FailureCode = "metadata_serialization_failed"
	// FailureDestinationMismatch — Drive confirmed that the uploaded
	// file is not parented by the folder resolved for this voiceover.
	// This is a hard integrity failure; no post-upload move is allowed.
	FailureDestinationMismatch FailureCode = "VOICEOVER_DESTINATION_MISMATCH"
	// FailureDestinationUnavailable — the requested voiceover destination
	// could not be resolved. Explicit requests never fall back to config
	// or historical roots.
	FailureDestinationUnavailable FailureCode = "VOICEOVER_DESTINATION_UNAVAILABLE"
	// FailureDBUnavailable — finalizeStage: voiceoverRepo is nil
	// (composition root did not wire it).
	FailureDBUnavailable FailureCode = "db_unavailable"
	// FailureTxBegin — finalizeStage: voiceoverRepo.BeginTx returned
	// an error (sqlite lock, schema drift, etc.).
	FailureTxBegin FailureCode = "tx_begin_failed"
	// FailureDBDelete — finalizeStage: DeleteByIDTx returned an
	// error (swap-mode preconditions).
	FailureDBDelete FailureCode = "db_delete_failed"
	// FailureDBInsert — finalizeStage: InsertTx returned an error.
	FailureDBInsert FailureCode = "db_insert_failed"
	// FailureOutboxEnqueue — finalizeStage:
	// outboxEnqueuer.EnqueueIndexEvent returned an error (indexing
	// deferred; row already in tx).
	FailureOutboxEnqueue FailureCode = "outbox_enqueue_failed"
	// FailureTxCommit — finalizeStage: tx.Commit returned an error.
	FailureTxCommit FailureCode = "tx_commit_failed"
	// FailureFinalize — the finalizer rejected the atomic persistence
	// command before commit. This is distinct from a commit failure.
	FailureFinalize FailureCode = "finalize_failed"
	// FailureReconciliationRequired (Audit P0.5, July 2026): the
	// post-commit verification surfaced a severe divergence — the
	// canonical voiceovers row itself is missing after the tx
	// committed. The audit-mandated typed FailureCode replaces
	// FailureTxCommit (which would mislabel the failure as a tx
	// commit error when in fact the tx did commit successfully).
	//
	// API surface contract: surfaced via BatchItem.Errors[]
	// (serialised into the API response shape per types.go audit-P01
	// closure contract). Operators reading the response can
	// distinguish post-commit-reconciliation-required from
	// actually-failed-commit via this typed literal.
	//
	// Grouped adjacent to FailureTxCommit for cognitive locality
	// (per code-reviewer recommendation): both surface from
	// finalizeStage's post-tx-execution scope; grep-targets cluster.
	FailureReconciliationRequired FailureCode = "reconciliation_required"
	// FailureDedupeAmbiguous (Step 7/12, June 2026) — finalizeStage:
	// the PR-VO-B3 dedupe gate observed >1 rows sharing the same
	// drive_file_id (the dedupe invariant — one canonical row per
	// DriveFileID — is broken). Fail-closed per godlike/07's
	// no-fake-availability policy: do NOT insert a duplicate row,
	// surface this code so operators investigate the ambiguous
	// state. Positioned here alongside the other finalizeStage
	// DB-scope failures (FailureDBUnavailable / FailureTxBegin /
	// FailureDBDelete / FailureDBInsert / FailureOutboxEnqueue /
	// FailureTxCommit) for semantic clustering.
	FailureDedupeAmbiguous FailureCode = "dedupe_ambiguous"
	// FailureInvalidSubfolder — processLanguage: SubfolderName path
	// traversal rejected by pathutil.SanitizeSubfolderSegment.
	FailureInvalidSubfolder FailureCode = "invalid_subfolder_name"
	// FailureInvalidFilename — processLanguage: SanitizeFilename
	// rejected the caller-supplied filename.
	FailureInvalidFilename FailureCode = "invalid_filename"
	// FailureDownload — preserved for back-compat with the legacy
	// service_test.go fixture at line 236.
	FailureDownload FailureCode = "download_failed"
	// FailureTimingIncompatible — publish stage: the timing policy is
	// required but the segment also requests silence removal. Raw
	// provider boundaries describe the PRE-clean audio; without an
	// edit-map remap the published timestamps would be fake. Fail
	// closed per godlike/07 (never represent unavailable timing as
	// a successful no-op).
	FailureTimingIncompatible FailureCode = "timing_incompatible_with_silence_removal"
	// FailureTimingUnavailable — publish stage: timing is required but
	// the TTS provider produced no word boundaries. The canonical
	// machine-readable surface is VOICEOVER_TIMING_UNAVAILABLE so
	// job/API layers never see a "SUCCEEDED but the data we thought
	// we had does not exist" outcome.
	FailureTimingUnavailable FailureCode = "VOICEOVER_TIMING_UNAVAILABLE"
	// FailureTimingBuild — publish stage: the canonical timing artifact
	// could not be assembled (hash, artifact validation, or SRT/VTT
	// projection rendering failed).
	FailureTimingBuild FailureCode = "timing_artifact_build_failed"
	// FailureTimingPublish — publish stage: a timing bundle file upload
	// failed AFTER the audio upload succeeded.
	FailureTimingPublish FailureCode = "timing_publish_failed"
)

// ─────────────────────────────────────────────────────────────────────────
// PR-VO-AUDIT-P05 (P0.5, July 2026): CompletionState typed enum for the
// post-commit verification outcome on FinalizeResult.
// ─────────────────────────────────────────────────────────────────────────
//
// CompletionState surfaces the durability signal that the existing
// bool-typed `Reused` field collapses: the verifier outcome after a
// successful finalize tx. Three explicit states:
//
//   - StateCompleted                  — verifier confirms both the
//     voiceovers row AND the media_assets projection durably present
//     (the canonical happy-path).
//   - StateCompletedUnverified        — verifier observed a warn-level
//     divergence (e.g. the media_assets projection missing while the
//     voiceovers row IS present). Caller may continue but should
//     surface to operators.
//   - StateReconciliationRequired     — verifier observed a severe
//     divergence (e.g. the voiceovers row itself missing). Per the
//     audit (P0.5), finalizeStage MUST NOT report the item as
//     StatusCompleted in this case; the canonical signal is the typed
//     CompletionState surfaced on FinalizeResult so callers can react.
//
// The pre-P0.5 surface log-only-and-continue silently degraded to
// `StateCompleted` because there was no typed truth sink on the
// caller side — only `Reused bool` was surfaced on FinalizeResult.
// Compliance with godlike/07 (no fake availability) now flows
// through this typed enum; godlike/06 (one canonical owner per fact)
// is preserved: CompletionState is the only emitter of the typo'd
// "completion_state" JSON key.
//
// JSON wire compat: `type CompletionState string` serialises into the
// same byte-for-byte strings as the constant values
// ("completed" / "completed_unverified" / "reconciliation_required").
// omitempty on the FinalizeResult field keeps the pre-P0.5 wire shape
// intact for callers that did not read the new field.
type CompletionState string

const (
	// StateCompleted — verifier confirms BOTH rows durably present.
	StateCompleted CompletionState = "completed"
	// StateCompletedUnverified — verifier observed a warn-level
	// divergence (e.g. media_assets projection missing but the
	// canonical voiceovers row present). tx committed successfully;
	// the missing secondary row is an audit signal, not a halt.
	StateCompletedUnverified CompletionState = "completed_unverified"
	// StateReconciliationRequired — verifier observed a severe
	// divergence (the canonical voiceovers row itself missing).
	// Caller must NOT report StatusCompleted; the canonical signal
	// is the typed CompletionState surfaced on FinalizeResult.
	StateReconciliationRequired CompletionState = "reconciliation_required"
)
