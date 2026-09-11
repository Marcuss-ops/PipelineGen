// Package scan — ssot_facts.go: the per-fact VOCABULARY consumed by the
// generic SSOT engine (ssot.go) and its registry (ssot_registry.go).
//
// This file owns everything that is specific to a fact — its rule id, its
// notes, its detection regexes, its canonical owner paths, and (for the facts
// that need cross-line state or a residue bucket) its per-file scanner. It
// owns NO walk, NO skip logic, NO snippet truncation and NO package
// extraction: the engine owns those once for every fact.
//
// Before the consolidation each fact below carried its own copy of that
// skeleton; the four stateful scanners at the bottom of this file were ~150
// to ~380 lines each, most of it the same walk/emit/truncate boilerplate.
package governance

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// ── Rule ids ───────────────────────────────────────────────────────────────
//
// These strings are the report.Rule values and are part of the observable
// contract (dashboards, the CI gate list and the per-gate tests pin them).
const (
	embeddingConstantsRule         = "percheck_embedding_constants_ssot"
	durationProbeSSOTRule          = "percheck_duration_probe_ssot"
	observabilityOperationSSOTRule = "percheck_observability_operation_ssot"
	speechTimingSSOTRule           = "percheck_speech_timing_ssot"
	projectDerivationSSOTRule      = "percheck_project_derivation_ssot"
	evidencePrecedenceSSOTRule     = "percheck_evidence_precedence_ssot"
	stopwordMapRule                = "percheck_stopword_maps_in_app"
	metadataKeyScannerRule         = "percheck_metadata_registry"
	indexedStateWriterSSOTRule     = "percheck_indexed_state_writer_ssot"
	assetCommitterEventSSOTRule    = "percheck_asset_committer_event_ssot"
)

// ── 1. Embedding model-id constants ───────────────────────────────────────
//
// godlike/06: the text-embedding identity facts (model id, revision,
// dimension) have ONE owner — internal/kernel/models. internal/kernel/embedding
// re-exports them via aliases. Historical drift (nomic-embed-text vs
// multilingual-e5-base) broke query/document vector coherence, so this gate
// fails closed on a re-introduced model-id declaration.
//
// Scope is deliberately narrow: DECLARATION lines only (`<ident> = "<model>"`,
// optionally preceded by const/var). Struct-literal fields (`Model: "..."`)
// are data flowing through the Qdrant schema / config surfaces, which the
// boot-time embedding-contract handshake already validates.
var embeddingModelIDLiteralRE = regexp.MustCompile(
	`^\s*(?:const|var)?\s*[A-Za-z_][A-Za-z0-9_]*\s*=\s*"(nomic-embed-text|intfloat/multilingual-e5-base|multilingual-e5-base|multilingual-e5-small|multilingual-e5-large)"`,
)

const embeddingConstantsNote = "forbidden embedding model-id declaration outside the canonical model-registry SSOT (PR-HASH-SEMANTICS item 16, August 2026); godlike/06 SSOT requires every text-embedding identity fact (model id, revision, dimension) to be owned ONLY by internal/kernel/models. Do NOT declare a new embedding-model constant/variable in another package — reference internal/kernel/models.CanonicalTextModelID (or the internal/kernel/embedding aliases) instead. Historical drift (nomic-embed-text vs multilingual-e5-base) broke query/document vector coherence; this gate fails closed on any re-introduction."

// ── 2. Duration probe (no raw ffprobe/ffmpeg spawn) ───────────────────────
//
// The canonical media capability (internal/platform/media/rustexec + the
// render probe adapter) is the single owner of media-binary execution. Every
// other package measures duration through rustexec.VideoProcessor.Probe and
// internal/kernel/asset.ResolveAssetDuration.
const durationProbeSSOTNote = "forbidden direct ffprobe/ffmpeg process spawn outside the canonical media probe capability (internal/platform/media/). Duration measurement MUST go through the canonical probe port (rustexec.VideoProcessor.Probe) and the kernel duration contract (internal/kernel/asset.ResolveAssetDuration); never a raw ffprobe/ffmpeg subprocess."

// durationProbeSSOTMatch reports whether a line spawns a raw ffprobe/ffmpeg
// process: an exec.Command(Context) call carrying the literal binary name.
func durationProbeSSOTMatch(line string) bool {
	lower := strings.ToLower(line)
	if !strings.Contains(lower, "exec.command") {
		return false
	}
	return strings.Contains(lower, `"ffprobe"`) || strings.Contains(lower, `"ffmpeg"`)
}

// ── 3. Observability operation (single writer + retired recorder) ─────────
//
// Prevents the two most dangerous regressions: reintroducing the retired
// measurement-recorder contract, or writing the performance read model from a
// second package. It deliberately does NOT ban infrastructure/health timers;
// those are outside run metrics.
const observabilityOperationCanonicalStore = "internal/platform/sqlite/performance/"

const (
	observabilityOperationRetiredRecorderNote = "retired MeasuredOperationRecorder; promote to OperationReport and use OperationReportProjectionRecorder"
	observabilityOperationSecondWriterNote    = "performance_operations is a read model; write it only through the canonical projection store"
)

// ── 4. Speech timing (canonical validated builder only) ───────────────────
//
// SpeechTimingArtifact literals must be built by
// internal/capabilities/audio/speech_artifact.go::BuildSpeechTimingArtifact,
// which assembles AND validates the provider-neutral word-boundary contract.
const speechTimingSSOTNote = "forbidden direct SpeechTimingArtifact struct-literal construction outside the canonical builder (internal/capabilities/audio/speech_artifact.go::BuildSpeechTimingArtifact). Consumers must treat the artifact as a read-only projection and build it only through the canonical validated constructor."

const speechTimingSSOTLiteral = "SpeechTimingArtifact{"

// ── 5. Project derivation (no hardcoded "scene" fallback) ─────────────────
//
// Project originates from internal/kernel/script.ArtifactRoutingContext and
// propagates verbatim; an empty project with a requested publish fails closed
// (ErrProjectRequired) instead of fabricating a namespace.
const projectDerivationSSOTNote = "forbidden hardcoded scene project-namespace derivation. Project must originate from the canonical routing context (internal/kernel/script.ArtifactRoutingContext) and propagate verbatim; an empty project with a requested publish fails closed (ErrProjectRequired) instead of silently falling back to a fabricated namespace."

// projectDerivationSSOTRe matches the retired `project = "scene"` fallback
// shapes (assignment, struct-literal field, comparison), case-insensitive.
var projectDerivationSSOTRe = regexp.MustCompile(`(?i)project\s*(?:=|:=|==)\s*"scene"`)

// ── 6. Evidence precedence (no transcript-first local helper) ─────────────
//
// The single owner is internal/kernel/asset/evidence.go::ResolveEvidence
// (transcript → semantic_summary → visual_summary → summary → description →
// fail closed). Producers build asset.EvidenceInput and call ResolveEvidence.
const evidencePrecedenceSSOTNote = "forbidden evidence-precedence re-implementation. The single owner is internal/kernel/asset/evidence.go::ResolveEvidence (transcript → semantic_summary → visual_summary → summary → description → fail closed). Build asset.EvidenceInput and call ResolveEvidence instead of re-ordering the tiers in a local first-non-empty helper."

// evidencePrecedenceSSOTHelperRe matches the selection-helper call shapes that
// historically drifted into per-consumer evidence-precedence copies.
var evidencePrecedenceSSOTHelperRe = regexp.MustCompile(`(?i)\b(firstString|firstNonEmpty|firstNonEmptyString|firstNonEmptyProvider|coalesce|pickFirst|firstEvidence)\s*\(`)

// evidencePrecedenceSSOTTranscriptRe pins the defining first tier: transcript
// is the tier whose presence marks an evidence precedence rather than an
// unrelated fallback (URLs, titles, provider ids).
var evidencePrecedenceSSOTTranscriptRe = regexp.MustCompile(`"transcript"`)

// evidencePrecedenceSSOTOtherTierRe matches any non-transcript canonical tier
// a transcript-first selection may be ordered against.
var evidencePrecedenceSSOTOtherTierRe = regexp.MustCompile(`"semantic_summary"|"visual_summary"|"summary"|"description"`)

// ── 7. Stopword maps (no hardcoded linguistic maps) ───────────────────────
//
// Stop-word sets MUST be loaded from the LexiconRegistry
// (internal/domain/linguistics/) at bootstrap. Any production file that
// defines a literal stop-word map is a godlike/06 SSOT violation — stop words
// are linguistic data, not code. The gate is SCOPED to the application and
// infrastructure trees, which is what exempts the canonical lexicon home.
const stopwordMapNote = "forbidden hardcoded stop-word map in application/infrastructure code. Stop-word sets MUST be loaded from the LexiconRegistry (internal/domain/linguistics/) at bootstrap via linguistics.DefaultLexicon().StopWords(). Hardcoded linguistic maps are a godlike/06 SSOT violation. See internal/domain/linguistics/lexicon_registry.go for the canonical approach."

// stopwordMapOpenRe matches a line that OPENS a hardcoded stop-word map
// literal: `map[string]struct{}{`, `map[string]bool{`, or the nested
// `map[string]map[string]struct{}{` form used by per-language marker maps. The
// pattern is deliberately loose so expanded multi-line literals (one quoted
// word per line — the codebase norm) are tracked by the brace-depth state
// machine in scanStopwordMapRuleFile instead of requiring words on the opener
// line.
var stopwordMapOpenRe = regexp.MustCompile(`map\[string\].*?(?:struct\{\}|bool)\{`)

// stopwordWordRe matches common stop-word-like quoted strings that appear as
// keys inside a hardcoded stop-word map literal.
var stopwordWordRe = regexp.MustCompile(`"the"|"and"|"for"|"with"|"from"|"that"|"this"`)

// scanStopwordMapRuleFile is the stateful stopword-map detector: it tracks the
// brace depth of an opened map literal and emits AT MOST ONE violation per
// map, anchored at the opener line, with the offending stop-word line as the
// snippet.
func scanStopwordMapRuleFile(_ any, path, relPath string, r *report.Report, rule *ssotRule) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	lineNo := 0
	inMap := false
	mapOpenLine := 0
	reported := false
	braceDepth := 0

	for sc.Scan() {
		lineNo++
		line := sc.Text()
		// Comment-only lines carry no structural map state; skip them
		// (godlike/07: descriptive prose is non-fatal residue).
		if ssotIsComment(line, ssotCommentPrefixes) {
			continue
		}

		if !inMap {
			if !stopwordMapOpenRe.MatchString(line) {
				continue
			}
			// Opener line: a single-line literal with stop-words on the
			// same line is a direct violation.
			if stopwordWordRe.MatchString(line) {
				ssotEmit(r, rule, relPath, lineNo, rule.MatchedRule,
					stopwordMapNote+" | snippet: "+truncateSSOTSnippet(line))
			}
			braceDepth = strings.Count(line, "{") - strings.Count(line, "}")
			if braceDepth > 0 {
				// Expanded multi-line literal: track the body until the
				// closing brace so one-word-per-line maps are caught.
				inMap = true
				mapOpenLine = lineNo
				reported = stopwordWordRe.MatchString(line)
			}
			continue
		}

		// Inside an opened stop-word map literal body.
		if !reported && stopwordWordRe.MatchString(line) {
			ssotEmit(r, rule, relPath, mapOpenLine, rule.MatchedRule,
				stopwordMapNote+" | snippet: "+truncateSSOTSnippet(line))
			reported = true
		}
		braceDepth += strings.Count(line, "{") - strings.Count(line, "}")
		if braceDepth <= 0 {
			inMap = false
		}
	}
}

// ── 8. Metadata-key registry (name-spaced Asset.Metadata alphabet) ────────
//
// Every name-spaced (`a.b.c`-containing) key in `Asset.Metadata[...]` literals
// or the typed accessor surface MUST be declared in the canonical registry.
// Bare keys (no dot) are residue-allowed for the migration window and surfaced
// via warnings; comment-only references are residue-accounted too. A missing
// or empty canonical registry is a fail-closed configuration violation.
const metadataKeyCanonicalPath = "internal/kernel/asset/detail/metadata_registry.go"

// metadataKeyScannerAccessRe matches BOTH direct `Metadata["X.Y.Z"]` index
// access AND the typed accessor surface (`GetMetadataString`, `GetMetadataInt`,
// `SetMetadataString`, `SetMetadataInt`, `MetadataBool`, `MetadataFloat`,
// `MetadataStringSlice`, `MetadataString`).
var metadataKeyScannerAccessRe = regexp.MustCompile(
	`(?:Metadata\[\s*|GetMetadata(?:String|Int|Bool|Float|StringSlice)\(\s*|SetMetadata(?:String|Int|Bool|Float|StringSlice)\(\s*|Metadata(?:Bool|Float|StringSlice|String)\(\s*)"([a-z][a-z0-9_.\-]*)"`,
)

// metadataKeyEntryRe extracts a SINGLE {Key, Owner, Type} struct-literal entry
// from a single line of the canonical registry file.
var metadataKeyEntryRe = regexp.MustCompile(
	`\{\s*Key:\s*"([^"]+)"\s*,\s*Owner:\s*"([^"]+)"\s*,\s*Type:\s*"([^"]+)"`,
)

// metadataKeyScannerNote is the violation Note string for unregistered
// name-spaced keys.
const metadataKeyScannerNote = "forbidden name-spaced Asset.Metadata key outside canonical registry (`internal/kernel/asset/detail/metadata_registry.go`); godlike/06 SSOT requires every `provider.*` style key to be declared in `allowedMetadataKeys` with `Owner` + `Type` before being written or read; bare keys (no dot) are residue-allowed for the migration window (file a follow-up PR to migrate them to the name-spaced surface via the typed-strip pipeline)"

// ParseMetadataKeys opens the canonical SOLE owner of the Asset.Metadata key
// whitelist and returns the alphabetical-sort of the parsed key strings. A
// missing or malformed canonical file surfaces as a typed CONFIGURATION_ERROR
// violation.
//
// godlike/07 fail-closed: the configuration errors are `error`-severity. A
// silent pass on a missing canonical file would convert the
// forward-prevention gate into an unconditional no-op.
func ParseMetadataKeys(root string, _ *policy.Policy, r *report.Report) []string {
	return parseMetadataKeys(root, r)
}

// parseMetadataKeys is the engine-facing implementation (the exported wrapper
// above keeps the historical entry point for callers/tests).
func parseMetadataKeys(root string, r *report.Report) []string {
	path := filepath.Join(root, metadataKeyCanonicalPath)
	f, err := os.Open(path)
	if err != nil {
		r.Violations = append(r.Violations, report.Violation{
			Package:     "internal/kernel/asset",
			File:        metadataKeyCanonicalPath,
			Line:        0,
			Rule:        metadataKeyScannerRule,
			Severity:    string(report.SeverityError),
			MatchedRule: "registry_canonical_missing",
			Note:        metadataKeyScannerNote + " | cannot open canonical registry: " + err.Error(),
		})
		return nil
	}
	defer f.Close()

	keys := []string{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	commentOnly := 0
	for sc.Scan() {
		line := sc.Text()
		m := metadataKeyEntryRe.FindStringSubmatch(line)
		if m == nil {
			if ssotIsComment(line, ssotCommentPrefixes) && strings.Contains(line, "Key:") {
				commentOnly++
			}
			continue
		}
		keys = append(keys, m[1])
	}

	if len(keys) == 0 {
		r.Violations = append(r.Violations, report.Violation{
			Package:     "internal/kernel/asset",
			File:        metadataKeyCanonicalPath,
			Line:        0,
			Rule:        metadataKeyScannerRule,
			Severity:    string(report.SeverityError),
			MatchedRule: "registry_canonical_empty",
			Note: metadataKeyScannerNote +
				" | canonical registry file present but no `{Key: ..., Owner: ..., Type: ...}` entries parsed — verify the file uses the single-line struct-literal format (PR-METADATA-REGISTRY-FOUNDATION, July 2026)",
		})
	}
	if commentOnly > 0 {
		r.Warnings = append(r.Warnings, metadataKeyScannerRule+" registry-config: "+
			strconv.Itoa(commentOnly)+" comment-only Key: reference(s) in "+
			metadataKeyCanonicalPath+
			" (descriptive prose; non-fatal per godlike/07)")
	}
	sort.Strings(keys)
	return keys
}

// metadataKeyHasConfigViolation reports whether parseMetadataKeys emitted a
// typed registry config violation, so the walk is skipped and the report does
// NOT flood with config-drift violations on top of the real cause.
func metadataKeyHasConfigViolation(r *report.Report) bool {
	for _, v := range r.Violations {
		if v.Rule == metadataKeyScannerRule &&
			(v.MatchedRule == "registry_canonical_missing" ||
				v.MatchedRule == "registry_canonical_empty") {
			return true
		}
	}
	return false
}

// scanMetadataKeysRuleFile walks one file's lines and emits an
// `unregistered_namespaced_key` violation per unknown name-spaced key. Bare
// keys and comment-only references are residue-accounted as warnings.
func scanMetadataKeysRuleFile(state any, path, relPath string, r *report.Report, rule *ssotRule) {
	whitelist, _ := state.(map[string]bool)

	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	lineNo := 0
	bareKeys := 0
	commentOnly := 0
	for sc.Scan() {
		lineNo++
		line := sc.Text()
		matches := metadataKeyScannerAccessRe.FindAllStringSubmatch(line, -1)
		if len(matches) == 0 {
			continue
		}
		isComment := ssotIsComment(line, ssotCommentPrefixes)
		for _, m := range matches {
			key := m[1]
			if isComment {
				// Comment-only reference: residue-accounted, NOT violated.
				commentOnly++
				continue
			}
			if !strings.Contains(key, ".") {
				// Bare key: residue-allowed for the migration window.
				bareKeys++
				continue
			}
			if whitelist[key] {
				continue
			}
			ssotEmit(r, rule, relPath, lineNo, "unregistered_namespaced_key",
				metadataKeyScannerNote+" | key: "+key)
		}
	}
	if bareKeys > 0 {
		ssotWarn(r, rule, "bare-key-residue:",
			strconv.Itoa(bareKeys)+" non-namespaced (bare) metadata-key reference(s) in "+relPath+
				" (legacy Asset.Metadata via typed-accessor; residue-allowed for the migration window per godlike/07)")
	}
	if commentOnly > 0 {
		ssotWarn(r, rule, "commentonly-residue:",
			strconv.Itoa(commentOnly)+" comment-only metadata-key reference(s) in "+relPath+
				" (descriptive prose; non-fatal per godlike/07 no-fake-availability)")
	}
}

// ── 9. Indexed-state writer (single canonical outbox consumer) ────────────
//
// The ONLY legitimate writer of media_assets.index_state='INDEXED' is the
// canonical outbox consumer chain
// (IndexingHandler → clipindexer.IndexClip → setIndexedAt). The gate bans the
// SQL write from any file outside the canonical writer packages, with a
// per-file `// INDEXED_WRITER_SCOPE: clipindexer` comment-marker allowlist.
const indexedStateWriterSSOTScopeMarker = "INDEXED_WRITER_SCOPE: clipindexer"

// indexedStateWriterSSOTCanonicalPaths lists the canonical INDEXED writer
// packages: setIndexedAt (SQLite/Qdrant mode) and PostgresIndexWorker
// (media-SSOT mode). Files under either prefix are exempt.
var indexedStateWriterSSOTCanonicalPaths = []string{
	"internal/platform/qdrant/indexing/clipindexer/",
	"internal/platform/postgres/media/",
}

const indexedStateWriterSSOTNote = "forbidden SQL write to media_assets.index_state='INDEXED' from a non-canonical file; the canonical INDEXED state transition is via the outbox consumer pipeline: IndexingHandler.Handle (internal/capabilities/jobs/outbox/indexing_handle.go) -> clipindexer.IndexClip (internal/platform/qdrant/indexing/clipindexer/indexing.go) -> setIndexedAt (internal/platform/qdrant/indexing/clipindexer/indexing_state.go, single atomic UPDATE with CAS fence on source_version + index_state='INDEXING'). Workflows MUST NOT bypass the outbox consumer; the only way to transition to INDEXED is via the canonical outbox consumer. _test.go files are exempt (regression-guard surface). The comment-marker `// INDEXED_WRITER_SCOPE: clipindexer` in a file header is the documented allowlist for edge cases (none today). Per godlike/06 SSOT (one canonical owner per fact), the only legitimate writer to index_state='INDEXED' is setIndexedAt. Per the user directive (Italian, July 2026): 'Fare in modo che lo stato asset.index.state=INDEXED passi solo dal consumer outbox dedicato.'"

// indexedStateWriterSSOTRe matches a literal INDEXED assignment in an SQL SET
// clause. Qualified read predicates such as `alias.index_state = 'INDEXED'`
// are projections/filters, not state transitions and must not be treated as
// writers.
var indexedStateWriterSSOTRe = regexp.MustCompile(`(?i)\bSET\s+(?:[a-z_][a-z0-9_]*\.)?index_state\s*=\s*['"]?INDEXED['"]?`)

var indexedStateWriterSSOTSetRe = regexp.MustCompile(`(?i)\bSET\b`)

var indexedStateWriterSSOTAssignmentRe = regexp.MustCompile(`(?i)(?:[a-z_][a-z0-9_]*\.)?index_state\s*=\s*['"]?INDEXED['"]?`)

// indexedStateWriterSSOTReferenceRe is used only for residue accounting in
// comments. It intentionally remains broader than the write matcher so
// descriptive references are still visible without becoming violations.
var indexedStateWriterSSOTReferenceRe = regexp.MustCompile(`(?i)index_state\s*=\s*['"]?INDEXED['"]?`)

// scanIndexedStateWriterRuleFile tracks a multi-line SQL SET clause so an
// INDEXED assignment spanning lines is caught, honours the comment-marker
// allowlist, and residue-accounts comment-only references.
func scanIndexedStateWriterRuleFile(_ any, path, relPath string, r *report.Report, rule *ssotRule) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	content, err := os.ReadFile(path)
	if err != nil {
		return
	}
	hasScopeMarker := strings.Contains(string(content), indexedStateWriterSSOTScopeMarker)

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	lineNo := 0
	commentOnly := 0
	insideSetClause := false
	for sc.Scan() {
		lineNo++
		line := sc.Text()
		// Residue accounting (godlike/07): comment-only references to the
		// SQL pattern are descriptive prose, not real writes.
		if ssotIsComment(line, ssotCommentPrefixes) && indexedStateWriterSSOTReferenceRe.MatchString(line) {
			commentOnly++
			continue
		}
		isIndexedAssignment := indexedStateWriterSSOTRe.MatchString(line) ||
			(insideSetClause && indexedStateWriterSSOTAssignmentRe.MatchString(line))
		if isIndexedAssignment && !hasScopeMarker {
			ssotEmit(r, rule, relPath, lineNo, rule.MatchedRule,
				indexedStateWriterSSOTNote+" | snippet: "+truncateSSOTSnippet(line))
		}
		if indexedStateWriterSSOTSetRe.MatchString(line) {
			insideSetClause = true
		}
		if insideSetClause && strings.Contains(line, ";") {
			insideSetClause = false
		}
	}
	if commentOnly > 0 {
		ssotWarn(r, rule, "indexed-state-writer-comments:",
			strconv.Itoa(commentOnly)+" comment-only reference(s) in "+relPath+
				" (descriptive prose; non-fatal per godlike/07 no-fake-availability)")
	}
}

// ── 10. Asset-committer event (single canonical emission site) ────────────
//
// The canonical `asset.index.requested` outbox event is created in EXACTLY
// ONE place: the canonical AssetCommitter chain (media_assets UPSERT + outbox
// INSERT in the SAME transaction). Any other production-code emission bypasses
// the commit pipeline and risks silent atomicity/duplicate-emission
// regressions. This gate protects EMISSION; percheck_identity_ssot protects
// DECLARATION.
const assetCommitterEventSSOTNote = "forbidden emission of canonical 'asset.index.requested' outbox-event literal outside the canonical AssetCommitter chain (PR-DIAGNOSI-FINALE rule 3, July 2026); godlike/06 SSOT requires the canonical envelope (asset.index.requested.v1) to be emitted ONLY by the AssetCommitter.CommitAsset pathway (internal/capabilities/assets/persistence/committer.go) via the mutations.AssetMutationDispatcher (atomic UPSERT + outbox INSERT in single TX, QDRANT-002 atomicity invariant). Any other emission site risks silent QDRANT-002 regression (Qdrant indexing a not-yet-committed media_assets row) or duplicate-emission regression (a future cleanup over-counts events per asset_id). Exempt zones per scanner policy: canonical AssetCommitter files, outboxevents constants package, mutations.Dispatcher envelope, provider services routing through AssetCommitter, finalizer/post-processing surfaces that emit via the canonical AssetMutationDispatcher envelope, CLI admin tools (cmd/admin/**), test fixtures (tests/**)."

// assetCommitterEventSSOTLiteralRe matches production-code emission of the
// literal `asset.index.requested` AND the canonical envelope
// `asset.index.requested.v1`.
var assetCommitterEventSSOTLiteralRe = regexp.MustCompile(`['"]asset\.index\.requested(\.v1)?['"]`)

// assetCommitterEventSSOTExemptPathPrefixes is the canonical exempt set —
// packages that legitimately reference the literal without bypassing the
// AssetCommitter chain. Every entry is verified to still hold at least one
// file containing the literal (godlike/08 zero-baseline: an exception list
// that lies is worse than none).
var assetCommitterEventSSOTExemptPathPrefixes = []string{
	// 1. The identity ssot owner — internal/kernel/event is the ONE package
	//    allowed to declare the literal (godlike/06 one owner per fact).
	"internal/kernel/event/",
	// 2. Canonical AssetCommitter files — the SOLE authority on the
	//    asset.index.requested emission site.
	"internal/capabilities/assets/persistence/",
	// 3. The canonical outboxevents package — re-exports the owner constant
	//    and documents the event family.
	"internal/platform/sqlite/outboxevents/",
	// 3b. The PostgreSQL media outbox adapter — engine mirror of the
	//     outboxevents re-exports (one fact family, two engine adapters).
	"internal/platform/postgres/media/",
	// 4. Finalizer / texttracks / voiceover / catalogsync / provider —
	//    surfaces that route through the canonical AssetCommitter pipeline.
	"internal/capabilities/assets/finalizer/",
	"internal/capabilities/assets/texttracks/",
	"internal/capabilities/voiceover/service/",
	"internal/capabilities/assets/soundeffect/",
	"internal/capabilities/assets/catalogsync/",
	"internal/capabilities/assets/providers/",
	// 5. Composition-root bundles — emit the canonical event_type literal
	//    only as typed documentation.
	"internal/app/",
	// 6. Idempotency keys package — the canonical asset.index.requested.v1
	//    envelope is documented at internal/kernel/idempotency/keys.go.
	"internal/kernel/idempotency/",
	// 7. CLI admin tools — operator tooling legitimately inspects and
	//    possibly emits asset.index.requested for data correction.
	"cmd/admin/",
	// 8. Image API surfaces — typed-port documentation referencing the
	//    canonical event_type literal.
	"internal/capabilities/images/",
	// 9. Metrics / observability — event_type labels for metric dimensions
	//    (NOT for emission).
	"internal/platform/observability/",
	// 10. Qdrant search dead-letter adapter — references the literal
	//     event_type for classification (NOT for emission).
	"internal/platform/qdrant/search/",
	// 11. Tests folder — regression-guard fixtures that legitimately
	//     reference the literal.
	"tests/",
}

// scanAssetCommitterEventRuleFile emits a violation per canonical-envelope
// emission and residue-accounts comment-only references (silenced in the
// operator-facing productionOnly mode).
func scanAssetCommitterEventRuleFile(_ any, path, relPath string, r *report.Report, rule *ssotRule) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	lineNo := 0
	commentOnly := 0
	for sc.Scan() {
		lineNo++
		line := sc.Text()
		if ssotIsComment(line, ssotCommentPrefixes) && assetCommitterEventSSOTLiteralRe.MatchString(line) {
			commentOnly++
			continue
		}
		if !assetCommitterEventSSOTLiteralRe.MatchString(line) {
			continue
		}
		ssotEmit(r, rule, relPath, lineNo, "non_canonical_index_event_emission",
			assetCommitterEventSSOTNote+" | snippet: "+truncateSSOTSnippet(line))
	}
	if commentOnly > 0 {
		ssotWarn(r, rule, "index-event-comments:",
			strconv.Itoa(commentOnly)+" comment-only reference(s) in "+relPath+
				" (descriptive prose; non-fatal per godlike/07 no-fake-availability)")
	}
}
