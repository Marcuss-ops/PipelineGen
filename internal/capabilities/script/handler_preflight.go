// Package script — handler_preflight.go owns POST /api/script/preflight.
//
// Why a server-side preflight (2026-11-23, Augias r42): the CLI already ran
// the same Stage 0 checks, but every other client of the pipeline (the console,
// a remote operator, a script in ops/) had to re-implement them or discover the
// failure minutes into a run. The route answers the same question the command
// asks — "can this exact body start, and does every clip it binds exist, is it
// ACTIVE/READY, does it have a known duration and, when the segment burns
// subtitles, a text track?" — using the SAME validator the submit path uses
// (bindGenerateEnvelope → PayloadValidator) so a green preflight and a 400 can
// never disagree about the contract.
//
// What it deliberately does NOT do: no TTS, no GPU, no render, no job row. The
// compiled plan (certified renders, media certificates, frame-exact windows)
// still needs the resolved plan and stays validated during the run.
//
// STOCK IS NO LONGER OUTSIDE THE GATE (CS27-06, 2026-11-27). The route used to
// verify clips only, so a payload whose stock source could not be admitted
// answered ok=true and died at 85% in PUBLISHING_DOCUMENTS with "stock Drive
// file \"…\" has no saved media registry entry". It now resolves every
// output.stock_bindings entry through the same media registry the handoff reads
// and reports each source with the exact sentence the run would have failed
// with. Bytes still stay Master-owned: this reads producer-side metadata, never
// the stock bytes, and never runs stockpipeline.
//
// Response contract:
//
//	200  ok=true   — the body is admissible and every clip/stock source passed;
//	400            — the canonical ingress error (unknown field, contract
//	                 violation): the same body the submit would reject;
//	422  ok=false  — the body is valid but at least one clip/asset/stock source
//	                 would fail; the report lists every one with its problems;
//	200  unverified=true — a reader is not wired; the contract was checked but
//	                 the catalog/registry was not. Never reported as ok, and
//	                 `verified` stays false so a green contract cannot be read as
//	                 a verified catalog.
package script

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// AssetPreflightFact is the narrow catalog view the gate reads. It mirrors the
// operator read model (GET /api/assets/operator/assets/:id) without importing
// it: the script capability must not depend on another capability's DTO.
type AssetPreflightFact struct {
	ClipID         string
	LifecycleState string
	AssetState     string
	DurationMS     int64
	TextTracks     int

	// Reachability facts (OVL-06): the resolver needs a local binary, or a
	// Drive reference when the segment allows a drive-only clip. Without
	// these the preflight could only say "asset non trovato" and leave the
	// operator without the path the resolver expects or the reason it is
	// unreachable.
	LocalPath    string
	DriveLink    string
	HasLocalFile bool
	HasDriveFile bool
}

// AssetPreflightReader resolves one clip's catalog facts. found=false means the
// id is not in the catalog (a 404 in the operator API), which is a preflight
// failure, never an empty success. A non-nil error means "not verified".
type AssetPreflightReader interface {
	PreflightAssetFacts(ctx context.Context, clipID string) (fact AssetPreflightFact, found bool, err error)
}

// Stock reference kinds are owned by the kernel walker (CollectStockReferences);
// the route re-exports them so adapters and tests name the same constants
// instead of re-typing the strings.
const (
	StockReferenceKindAsset     = scriptpkg.StockReferenceKindAsset
	StockReferenceKindDriveFile = scriptpkg.StockReferenceKindDriveFile
	StockReferenceKindFolder    = scriptpkg.StockReferenceKindFolder
	StockReferenceKindAmbiguous = scriptpkg.StockReferenceKindAmbiguous
	StockReferenceKindInvalid   = scriptpkg.StockReferenceKindInvalid
)

// StockPreflightRef names one stock source by the SINGLE identity the payload
// declared. Kind is one of scriptpkg.StockReferenceKindAsset / ...DriveFile: a
// binding that names two sources is rejected before it reaches the reader.
type StockPreflightRef struct {
	Kind string
	ID   string
}

// StockPreflightFact is the narrow media-registry view the stock gate reads. It
// mirrors what the remote handoff needs from the Producer: a saved duration and
// a usable content hash. Without both, remote_final_job_resolve.go refuses the
// handoff, so reporting them here is the difference between a preflight and a
// postmortem.
//
// The hash is carried as a LENGTH, not as a value: this fact already names a
// location (DriveFileID), and the media-identity rule forbids one struct from
// carrying a location next to a content address. The Producer never uses the
// digest itself — only whether the row has a certifiable one.
type StockPreflightFact struct {
	DriveFileID      string
	Name             string
	DurationUS       int64
	SizeBytes        int64
	ContentSHA256Len int
}

// StockPreflightReader resolves one stock reference against the media registry.
// found=false means the registry has no row for that source, which is exactly
// the r14 failure; a non-nil error means "not verified".
type StockPreflightReader interface {
	PreflightStockFacts(ctx context.Context, ref StockPreflightRef) (fact StockPreflightFact, found bool, err error)
}

// StockFolderPreflightReader resolves a Drive folder id to its name so the gate
// can name a folder that does not belong to the declared project (r12: the
// payload pointed at the Augias folder while the project was DeGennaro).
type StockFolderPreflightReader interface {
	PreflightStockFolderName(ctx context.Context, folderID string) (name string, found bool, err error)
}

// PreflightDeps is the optional dependency bag for POST /preflight. A nil
// Assets reader keeps the route mounted (contract-only) and reports
// unverified=true; the same rule applies to Stock.
type PreflightDeps struct {
	Assets  AssetPreflightReader
	Stock   StockPreflightReader
	Folders StockFolderPreflightReader
}

// assetPreflightCheck is the per-clip JSON verdict.
type assetPreflightCheck struct {
	ClipID     string   `json:"clip_id"`
	OK         bool     `json:"ok"`
	Scenes     []string `json:"scenes,omitempty"`
	DurationMS int64    `json:"duration_ms,omitempty"`
	Problems   []string `json:"problems,omitempty"`
	// Warnings are degradations the render can still survive (a clip with a
	// Drive copy but no local binary). They never flip ok, so a warning is
	// never read as a pass either: the two lists stay distinct in the JSON.
	Warnings []string `json:"warnings,omitempty"`
}

// stockPreflightCheck is the per-source JSON verdict. Resolved mirrors the
// asset gate: a source is resolved only when the registry produced the facts
// the handoff needs.
//
// Reference is ONE string ("asset:<id>", "drive_file:<id>", "folder:<id>") on
// purpose: a binding names one source, and a report that could print an asset id
// next to a Drive id would be the same dual truth the media-identity rule bans.
type stockPreflightCheck struct {
	ItemID    string `json:"item_id,omitempty"`
	Kind      string `json:"kind"` // "binding" | "pool"
	Index     int    `json:"index,omitempty"`
	Scene     string `json:"scene,omitempty"`
	SegmentID string `json:"segment_id,omitempty"`
	// Reference is the source the payload declared.
	Reference string `json:"reference,omitempty"`
	// ResolvedReference is the Drive identity the registry row added, when the
	// payload selected by asset id and the handoff needs the file.
	ResolvedReference string   `json:"resolved_reference,omitempty"`
	Resolved          bool     `json:"resolved"`
	Certified         bool     `json:"certified"`
	DurationUS        int64    `json:"duration_us,omitempty"`
	Warnings          []string `json:"warnings,omitempty"`
	Problems          []string `json:"problems,omitempty"`
}

// preflightReport is the JSON body of the route.
type preflightReport struct {
	OK     bool                  `json:"ok"`
	Items  int                   `json:"items"`
	Clips  int                   `json:"clips"`
	Assets []assetPreflightCheck `json:"assets,omitempty"`
	// StockRefs counts the declared stock sources, Stock carries the verdict of
	// each one. A payload with stock but no wired reader is unverified, never ok.
	StockRefs int                   `json:"stock_refs"`
	Stock     []stockPreflightCheck `json:"stock,omitempty"`
	Errors    []string              `json:"errors,omitempty"`

	// Verified is true only when every declared source was actually read. It
	// never claims the sources are healthy: Ok answers that.
	Verified bool `json:"verified"`

	// Unverified is set when no catalog/registry reader is wired: the contract
	// was checked, the sources were not. It is explicit so a green contract can
	// never be read as a verified catalog.
	Unverified bool `json:"unverified,omitempty"`
}

// PreflightRoute registers POST /preflight on the script router group.
func (h *HandlerGenerate) PreflightRoute(r *gin.RouterGroup) {
	if h == nil {
		return
	}
	r.POST("/preflight", h.Preflight)
}

// SetAssetPreflight wires the catalog reader used by POST /preflight. It is a
// setter (not a constructor argument) so every existing NewHandlerGenerate
// caller — including the test fixtures — keeps its signature.
func (h *HandlerGenerate) SetAssetPreflight(reader AssetPreflightReader) {
	if h == nil {
		return
	}
	h.preflightAssets = reader
}

// SetStockPreflight wires the media-registry reader (and, when available, the
// Drive folder reader) used by POST /preflight for stock sources. Both are
// optional, and an unwired reader is reported as unverified instead of being
// treated as a clean inventory.
func (h *HandlerGenerate) SetStockPreflight(reader StockPreflightReader, folders StockFolderPreflightReader) {
	if h == nil {
		return
	}
	h.preflightStock = reader
	h.preflightFolders = folders
}

// Preflight handles POST /api/script/preflight.
func (h *HandlerGenerate) Preflight(c *gin.Context) {
	// Same binding + validator as POST /generate: identical 400s, identical
	// unknown-field and retired-field handling.
	env, ok := bindGenerateEnvelope(c, h.validator)
	if !ok {
		return
	}
	report := h.buildPreflightReport(c.Request.Context(), env)

	status := http.StatusOK
	if !report.OK && !report.Unverified {
		status = http.StatusUnprocessableEntity
	}
	c.JSON(status, report)
}

// buildPreflightReport is the whole verdict of the route: contract facts
// (clips, stock sources) measured against the wired readers, never a render.
func (h *HandlerGenerate) buildPreflightReport(ctx context.Context, env *scriptpkg.GenerationEnvelopeV2) preflightReport {
	report := preflightReport{Items: len(env.Items)}
	reqs := scriptpkg.CollectClipRequirements(env)
	report.Clips = len(reqs)
	h.collectPreflightAssets(ctx, reqs, &report)
	h.collectPreflightStock(ctx, env, &report)
	report.OK = len(report.Errors) == 0 && !report.Unverified
	report.Verified = !report.Unverified
	return report
}

// collectPreflightAssets fills the per-clip section. A nil catalog reader is
// reported as unverified, never as a green contract.
func (h *HandlerGenerate) collectPreflightAssets(ctx context.Context, reqs []scriptpkg.ClipRequirement, report *preflightReport) {
	switch {
	case len(reqs) == 0:
		// No clip bound (pure text run): nothing to verify in the catalog.
	case h.preflightAssets == nil:
		report.Unverified = true
		report.Errors = append(report.Errors, fmt.Sprintf(
			"asset: catalogo non collegato a questa istanza — %d clip(s) non verificati", len(reqs)))
	default:
		report.Assets = h.checkPreflightAssets(ctx, reqs)
		for _, check := range report.Assets {
			if check.OK {
				continue
			}
			where := check.ClipID
			if len(check.Scenes) > 0 {
				where += " (" + strings.Join(check.Scenes, ", ") + ")"
			}
			report.Errors = append(report.Errors, "asset "+where+": "+strings.Join(check.Problems, "; "))
		}
	}
}

// collectPreflightStock fills the per-source section. A stock source the Master
// cannot admit is exactly as fatal as a missing clip, and the route used to
// answer ok=true for both (CS27-06, r12/r14).
func (h *HandlerGenerate) collectPreflightStock(ctx context.Context, env *scriptpkg.GenerationEnvelopeV2, report *preflightReport) {
	refs := scriptpkg.CollectStockReferences(env)
	report.StockRefs = len(refs)
	switch {
	case len(refs) == 0:
		// No stock bound: nothing to verify in the registry.
	case h.preflightStock == nil:
		report.Unverified = true
		report.Errors = append(report.Errors, fmt.Sprintf(
			"stock: registro stock non collegato a questa istanza — %d riferimento/i non verificati", len(refs)))
	default:
		report.Stock = h.checkPreflightStock(ctx, refs)
		for _, check := range report.Stock {
			if len(check.Problems) == 0 {
				continue
			}
			report.Errors = append(report.Errors, "stock "+stockCheckLabel(check)+": "+strings.Join(check.Problems, "; "))
		}
	}
}

// checkPreflightAssets resolves every requirement sequentially. The
// architecture gate forbids raw goroutines in the transport layer
// (TestStaticGate_NoConcreteInfrastructureInTransport), and a catalog read per
// clip is a single indexed lookup: a 22-clip payload is one short request, not
// a fan-out that would need bounding. Parallelism, when it is needed, belongs
// behind the port (a read model that batches), not in the handler.
func (h *HandlerGenerate) checkPreflightAssets(ctx context.Context, reqs []scriptpkg.ClipRequirement) []assetPreflightCheck {
	out := make([]assetPreflightCheck, 0, len(reqs))
	for _, req := range reqs {
		out = append(out, preflightAssetCheck(ctx, h.preflightAssets, req))
	}
	return out
}

// preflightAssetCheck is the pure verdict for one clip, split from the HTTP
// handler so the rules can be unit-tested without a router or a database.
func preflightAssetCheck(ctx context.Context, reader AssetPreflightReader, req scriptpkg.ClipRequirement) (check assetPreflightCheck) {
	check = assetPreflightCheck{ClipID: req.ClipID, Scenes: append([]string(nil), req.Scenes...)}
	if reader == nil {
		check.Problems = []string{"catalogo non collegato"}
		return check
	}
	fact, found, err := reader.PreflightAssetFacts(ctx, req.ClipID)
	if err != nil {
		check.Problems = []string{"catalogo non consultabile: " + err.Error()}
		return check
	}
	if !found {
		check.Problems = []string{"asset non trovato nel catalogo: nessuna riga in media_assets per l'id (atteso local_path o drive_link)"}
		return check
	}
	check.DurationMS = fact.DurationMS
	if !strings.EqualFold(strings.TrimSpace(fact.LifecycleState), "ACTIVE") {
		check.Problems = append(check.Problems, "lifecycle_state="+fallbackValue(fact.LifecycleState, "sconosciuto")+" (serve ACTIVE)")
	}
	if !strings.EqualFold(strings.TrimSpace(fact.AssetState), "READY") {
		check.Problems = append(check.Problems, "asset_state="+fallbackValue(fact.AssetState, "sconosciuto")+" (serve READY)")
	}
	if fact.DurationMS <= 0 {
		check.Problems = append(check.Problems, "durata sconosciuta (duration_ms<=0: il renderer non ha una finestra valida)")
	}
	// A declared editorial window must fit the catalog copy (CS27-06 step 3.4):
	// a clip cut for 52,01 s and used as 53,01 s used to fail mid-render. The
	// check only runs when the payload declares the window, so payloads that
	// derive it from the asset are never judged against a guess.
	if req.DurationMS > 0 && fact.DurationMS > 0 {
		if drift := preflightDurationDriftMS(req.DurationMS, fact.DurationMS); drift > preflightClipDurationToleranceMS {
			check.Problems = append(check.Problems, fmt.Sprintf(
				"durata richiesta %dms vs %dms nel catalogo (tolleranza %dms): la clip non copre la finestra del piano",
				req.DurationMS, fact.DurationMS, preflightClipDurationToleranceMS))
		}
	}
	if req.NeedSpeech && fact.TextTracks == 0 {
		check.Problems = append(check.Problems, "NO_SPEECH: nessuna traccia testo (il render sottotitoli fallirà dopo l'ASR)")
	}
	// Reachability is the OVL-06 verdict: a clip with neither a local binary
	// nor a Drive copy is a fatal preflight failure, named with the path the
	// resolver expects; a Drive-only clip is a warning, not a pass and not a
	// block.
	reachProblems, reachWarnings := scriptpkg.ClipMediaProblems(scriptpkg.ClipMediaReachability{
		ClipID:       req.ClipID,
		LocalPath:    fact.LocalPath,
		DriveLink:    fact.DriveLink,
		HasLocalFile: fact.HasLocalFile,
		HasDriveFile: fact.HasDriveFile,
	})
	check.Problems = append(check.Problems, reachProblems...)
	check.Warnings = append(check.Warnings, reachWarnings...)
	check.OK = len(check.Problems) == 0
	return check
}

func fallbackValue(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

// preflightClipDurationToleranceMS bounds the drift a declared clip window may
// have against the catalog before it is a problem. It covers container and
// frame rounding (one frame at 24 fps is 42 ms), not the one-second mismatch
// CS27-06 recorded.
const preflightClipDurationToleranceMS = 250

func preflightDurationDriftMS(requested, catalog int64) int64 {
	drift := requested - catalog
	if drift < 0 {
		return -drift
	}
	return drift
}

// checkPreflightStock resolves every declared stock source sequentially, for
// the same reason checkPreflightAssets does: a registry lookup is one indexed
// read, and fanning out would need bounding for no measured benefit. Reported
// warning lookups (the Drive folder name) are cached per folder.
func (h *HandlerGenerate) checkPreflightStock(ctx context.Context, refs []scriptpkg.StockReference) []stockPreflightCheck {
	out := make([]stockPreflightCheck, 0, len(refs))
	folders := make(map[string]string, len(refs))
	for _, ref := range refs {
		out = append(out, h.preflightStockCheck(ctx, ref, folders))
	}
	return out
}

// preflightStockCheck is the pure verdict for one stock source, split from the
// HTTP handler so the rules can be unit-tested without a router or a database.
func (h *HandlerGenerate) preflightStockCheck(ctx context.Context, ref scriptpkg.StockReference, folders map[string]string) stockPreflightCheck {
	check := stockPreflightCheck{
		ItemID:    ref.ItemID,
		Kind:      stockReferenceKind(ref),
		Index:     ref.Index,
		Scene:     ref.SceneID,
		SegmentID: ref.SegmentID,
		Reference: ref.String(),
	}
	switch ref.Kind {
	case "":
		check.Problems = append(check.Problems, "nessuna sorgente stock: serve esattamente uno tra asset_id, drive_link e folder_id")
		return check
	case scriptpkg.StockReferenceKindAmbiguous:
		check.Problems = append(check.Problems, "riferimento ambiguo: il binding dichiara più sorgenti ("+strings.Join(ref.Declared, ", ")+") ma il piano ne accetta una sola")
		return check
	case scriptpkg.StockReferenceKindInvalid:
		check.Problems = append(check.Problems, "drive_link non è un URL Drive di file: "+ref.ID)
		return check
	case scriptpkg.StockReferenceKindFolder:
		// The pool is Master-expanded: the file identity is unknown here, so the
		// only producer-side verdict is the folder it names.
		check.Warnings = append(check.Warnings, h.stockFolderWarnings(ctx, ref, folders)...)
		return check
	}
	fact, found, err := h.preflightStock.PreflightStockFacts(ctx, StockPreflightRef{Kind: ref.Kind, ID: ref.ID})
	if err != nil {
		check.Problems = append(check.Problems, "registro stock non consultabile: "+err.Error())
		return check
	}
	if !found {
		check.Problems = append(check.Problems, stockReferenceMissingMessage(ref))
		return check
	}
	check.Resolved = true
	check.DurationUS = fact.DurationUS
	check.Certified = fact.ContentSHA256Len == 64
	if ref.Kind == scriptpkg.StockReferenceKindAsset && fact.DriveFileID != "" {
		check.ResolvedReference = scriptpkg.StockReferenceKindDriveFile + ":" + fact.DriveFileID
	}
	if fact.DurationUS <= 0 {
		check.Problems = append(check.Problems, "durata sconosciuta nel registro (duration_us<=0: il Master non ha una finestra valida)")
	}
	if !check.Certified {
		check.Problems = append(check.Problems, "SHA-256 assente nel registro: il riferimento non è certificabile dal Master")
	}
	return check
}

func stockReferenceKind(ref scriptpkg.StockReference) string {
	if ref.PoolSlot {
		return "pool"
	}
	return "binding"
}

// stockReferenceMissingMessage reproduces the exact handoff failure
// (remote_final_job_resolve.go) so an operator reading the preflight sees the
// sentence the run would have died with, plus the id to act on.
func stockReferenceMissingMessage(ref scriptpkg.StockReference) string {
	if ref.Kind == scriptpkg.StockReferenceKindDriveFile {
		return fmt.Sprintf("stock Drive file %q has no saved media registry entry; run the stockpipeline first", ref.ID)
	}
	return fmt.Sprintf("stock asset %q non è nel media registry: seleziona un asset ingerito oppure referenzia il file Drive", ref.ID)
}

func stockCheckLabel(check stockPreflightCheck) string {
	parts := make([]string, 0, 3)
	if check.ItemID != "" {
		parts = append(parts, check.ItemID)
	}
	if check.SegmentID != "" {
		parts = append(parts, check.SegmentID)
	} else if check.Scene != "" {
		parts = append(parts, check.Scene)
	}
	id := check.Reference
	if id == "" {
		id = fmt.Sprintf("binding-%d", check.Index)
	}
	parts = append(parts, id)
	return strings.Join(parts, "/")
}

// stockFolderWarnings names a folder that cannot belong to the declared project
// (r12: the payload pointed at the Augias folder while the project was
// DeGennaro). It is a warning, never a verdict: Drive folder naming is
// editorial, so a mismatch asks the operator to check rather than refusing a
// payload that may be perfectly deliberate.
func (h *HandlerGenerate) stockFolderWarnings(ctx context.Context, ref scriptpkg.StockReference, folders map[string]string) []string {
	name, cached := folders[ref.ID]
	if !cached && h.preflightFolders != nil {
		resolved, found, err := h.preflightFolders.PreflightStockFolderName(ctx, ref.ID)
		if err != nil {
			folders[ref.ID] = ""
			return []string{"cartella Drive " + ref.ID + " non verificabile: " + err.Error()}
		}
		if found {
			name = strings.TrimSpace(resolved)
		}
		folders[ref.ID] = name
	}
	project := strings.TrimSpace(ref.Project)
	if name == "" || project == "" {
		return nil
	}
	if stockFolderMatchesProject(name, project) {
		return nil
	}
	return []string{fmt.Sprintf(
		"la cartella Drive %q (%s) non appartiene al progetto dichiarato %q: verifica la folder_id",
		name, ref.ID, project)}
}

// stockFolderMatchesProject reports whether the Drive folder name echoes at
// least one significant token of the declared project. Tokens shorter than four
// characters are dropped ("de", "il"), and the boilerplate a run name carries
// is not required to appear in the folder title.
func stockFolderMatchesProject(folderName, project string) bool {
	tokens := stockProjectTokens(project)
	if len(tokens) == 0 {
		return true
	}
	normalized := normalizeStockToken(folderName)
	if normalized == "" {
		return true
	}
	for _, token := range tokens {
		if strings.Contains(normalized, token) {
			return true
		}
	}
	return false
}

func normalizeStockToken(value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(value) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// stockProjectBoilerplateTokens are the words a run name shares with any
// project, so matching on them would prove nothing.
var stockProjectBoilerplateTokens = map[string]struct{}{
	"compilation": {},
	"final":       {},
	"video":       {},
	"videos":      {},
	"stock":       {},
	"assets":      {},
	"drive":       {},
	"project":     {},
	"edit":        {},
}

func stockProjectTokens(project string) []string {
	fields := strings.FieldsFunc(strings.ToLower(project), func(r rune) bool {
		return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'))
	})
	tokens := make([]string, 0, len(fields))
	for _, field := range fields {
		if len(field) < 4 {
			continue
		}
		if _, boilerplate := stockProjectBoilerplateTokens[field]; boilerplate {
			continue
		}
		tokens = append(tokens, field)
	}
	return tokens
}
