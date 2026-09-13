// Package scan — companion test for percheck_media_write_bridge_ban.go.
//
// Pins:
//
//	(a) "violation trip" — the direct form `repo.Upsert(ctx, a)` on a
//	    detail.Repository field emits a violation.
//	(b) "service form" — `svc.Save(ctx, d)` / `svc.Delete(ctx, id)` on a
//	    detail.Service field emits a violation.
//	(c) "chained repository form" — `svc.Repository().Upsert(...)` is caught,
//	    because that chain is how ClipsRegistry and the ingest lifecycle reach
//	    the repository write boundary.
//	(d) "read is not this gate" — Get/List/Count/FindByExternalRef are reads
//	    owned by percheck_sqlite_media_reader_ban and must NOT be flagged.
//	(e) "narrow port is exempt" — a file that declares a narrow port and calls
//	    a write on it (an AssetCommitter, a mutations dispatcher) is clean; the
//	    gate bans the GENERIC seam, not every interface method named Upsert.
//	(f) "zone exempt" — the detail definition and the canonical PostgreSQL
//	    package are exempt by construction.
//	(g) "register exempt by exact path" — a registered file is exempt, a NEW
//	    sibling in the same package is not.
//	(h) "alias resolved through the import" — only the real detail package's
//	    types count, so an unrelated `detail` identifier is not a violation.
//	(i) the REAL repository tree is clean under this gate, and the debt register
//	    has neither ghost nor stale entries.
package boundaries

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

func scanMediaWriteBridge(t *testing.T, root string) *report.Report {
	t.Helper()
	r := &report.Report{}
	ScanMediaWriteBridgeBan(root, &policy.Policy{}, r)
	return r
}

func mediaWriteBridgeViolations(r *report.Report) []report.Violation {
	var out []report.Violation
	for _, v := range r.Violations {
		if v.Rule == mediaWriteBridgeRule {
			out = append(out, v)
		}
	}
	return out
}

// allRegisteredMediaWriteBridgeFiles is the union of both registers, so a new
// entry in either map is automatically covered by the existence, staleness and
// exemption pins.
func allRegisteredMediaWriteBridgeFiles() []string {
	out := make([]string, 0, len(mediaWriteBridgeGrandfatheredFiles)+len(mediaWriteBridgeDegradeOnlyFiles))
	for rel := range mediaWriteBridgeGrandfatheredFiles {
		out = append(out, rel)
	}
	for rel := range mediaWriteBridgeDegradeOnlyFiles {
		out = append(out, rel)
	}
	return out
}

// detailSeamHeader is the minimal import block that makes a test fixture's
// detail.Repository / detail.Service spellings the real kernel seam.
const detailSeamHeader = `package fixture

import "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"

`

// TestMediaWriteBridgeRegistersAreDisjoint pins that the two registers encode
// different facts: production split-brain debt vs. a degrade path selected only
// when the media SSOT is closed.
func TestMediaWriteBridgeRegistersAreDisjoint(t *testing.T) {
	for rel := range mediaWriteBridgeGrandfatheredFiles {
		if mediaWriteBridgeDegradeOnlyFiles[rel] {
			t.Errorf("%q is in BOTH the debt register and the degrade-only register — pick one (it is either wrong and must be migrated, or it is a documented media-disabled path)", rel)
		}
	}
}

// TestScanMediaWriteBridgeBan_ViolationTrip is the load-bearing test: a NEW
// production media write through the generic seam MUST fail the gate. Without
// it the scanner could be a silent no-op.
func TestScanMediaWriteBridgeBan_ViolationTrip(t *testing.T) {
	tmp := t.TempDir()
	writeGoFile(t, tmp, "internal/capabilities/nouveau/writer.go", detailSeamHeader+`type svc struct {
	repo detail.Repository
}

func (s *svc) Persist(ctx any, a any) error {
	return s.repo.Upsert(ctx, a)
}
`)

	r := scanMediaWriteBridge(t, tmp)
	got := mediaWriteBridgeViolations(r)
	if len(got) != 1 {
		t.Fatalf("violations = %d, want exactly 1: %+v", len(got), got)
	}
	if got[0].MatchedRule != "forbidden_seam_media_write" {
		t.Errorf("MatchedRule = %q, want forbidden_seam_media_write", got[0].MatchedRule)
	}
	if got[0].Line == 0 {
		t.Errorf("expected a non-zero line number, got %+v", got[0])
	}
}

// TestScanMediaWriteBridgeBan_AllRepositoryWritesCaught pins every mutating
// method of the repository seam, so none can be smuggled back in.
func TestScanMediaWriteBridgeBan_AllRepositoryWritesCaught(t *testing.T) {
	tmp := t.TempDir()
	writeGoFile(t, tmp, "internal/capabilities/nouveau/all_writes.go", detailSeamHeader+`type svc struct {
	repo detail.Repository
}

func (s *svc) Do(ctx any, id string) {
	_ = s.repo.Upsert(ctx, nil)
	_ = s.repo.SoftDelete(ctx, id)
	_ = s.repo.Restore(ctx, id)
	_ = s.repo.HardDelete(ctx, id)
}
`)

	got := mediaWriteBridgeViolations(scanMediaWriteBridge(t, tmp))
	if len(got) != 4 {
		t.Fatalf("violations = %d, want 4 (one per mutating method): %+v", len(got), got)
	}
}

// TestScanMediaWriteBridgeBan_ServiceWriteCaught pins the detail.Service write
// methods, which have different names from the repository seam's.
func TestScanMediaWriteBridgeBan_ServiceWriteCaught(t *testing.T) {
	tmp := t.TempDir()
	writeGoFile(t, tmp, "internal/capabilities/nouveau/service_write.go", detailSeamHeader+`type svc struct {
	assets *detail.Service
}

func (s *svc) Save(ctx any, d any) error {
	return s.assets.Save(ctx, d)
}

func (s *svc) Remove(ctx any, id string) error {
	return s.assets.Delete(ctx, id)
}
`)

	got := mediaWriteBridgeViolations(scanMediaWriteBridge(t, tmp))
	if len(got) != 2 {
		t.Fatalf("violations = %d, want 2: %+v", len(got), got)
	}
}

// TestScanMediaWriteBridgeBan_ChainedRepositoryCaught pins the
// `svc.Repository().Upsert(...)` form used by the clips registry and the ingest
// lifecycle. Missing it would leave the gate blind to its most common shape.
func TestScanMediaWriteBridgeBan_ChainedRepositoryCaught(t *testing.T) {
	tmp := t.TempDir()
	writeGoFile(t, tmp, "internal/capabilities/nouveau/chained.go", detailSeamHeader+`type svc struct {
	assets *detail.Service
}

func (s *svc) Do(ctx any) error {
	return s.assets.Repository().Upsert(ctx, nil)
}
`)

	got := mediaWriteBridgeViolations(scanMediaWriteBridge(t, tmp))
	if len(got) != 1 {
		t.Fatalf("chained seam write must be flagged, got %d violations: %+v", len(got), got)
	}
}

// TestScanMediaWriteBridgeBan_RegressionYouTubeEnrichment is the named
// regression for the defect this gate exists to prevent. youtube/usecase held a
// `detail.Repository` field, called Upsert from metadata enrichment, and in
// PostgreSQL mode the composition root had satisfied that interface with the
// SQLite AssetStoreSQLite facade — so enrichment wrote media_assets on the
// wrong engine while every SQL-level gate stayed green. The fixture is the
// shape of that file; the gate MUST flag it.
func TestScanMediaWriteBridgeBan_RegressionYouTubeEnrichment(t *testing.T) {
	tmp := t.TempDir()
	writeGoFile(t, tmp, "internal/capabilities/youtube/usecase/service.go", `package usecase

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

type Service struct {
	assetRepo detail.Repository
}

func (s *Service) enrich(ctx context.Context, a *asset.Asset) error {
	return s.assetRepo.Upsert(ctx, a)
}
`)

	got := mediaWriteBridgeViolations(scanMediaWriteBridge(t, tmp))
	if len(got) != 1 {
		t.Fatalf("the historical YouTube enrichment write shape must be flagged, got %d: %+v", len(got), got)
	}
	if !strings.Contains(got[0].Note, "detail.Repository") || !strings.Contains(got[0].Note, "Upsert") {
		t.Errorf("violation Note must name the seam and the method, got %q", got[0].Note)
	}
}

// TestScanMediaWriteBridgeBan_ReadsAreNotFlagged pins the single-owner split:
// reads belong to percheck_sqlite_media_reader_ban, so this gate must not
// double-report them.
func TestScanMediaWriteBridgeBan_ReadsAreNotFlagged(t *testing.T) {
	tmp := t.TempDir()
	writeGoFile(t, tmp, "internal/capabilities/nouveau/reader.go", detailSeamHeader+`type svc struct {
	repo detail.Repository
	assets *detail.Service
}

func (s *svc) Read(ctx any, id string) {
	_, _ = s.repo.Get(ctx, id)
	_, _ = s.repo.List(ctx, nil)
	_, _ = s.repo.Count(ctx, nil)
	_, _ = s.repo.FindByExternalRef(ctx, "youtube", "x")
	_, _ = s.assets.Get(ctx, id)
	_, _ = s.assets.List(ctx, nil)
}
`)

	if got := mediaWriteBridgeViolations(scanMediaWriteBridge(t, tmp)); len(got) != 0 {
		t.Fatalf("media reads are the reader gate's business, got %d: %+v", len(got), got)
	}
}

// TestScanMediaWriteBridgeBan_NarrowPortExempt pins that the gate bans the
// GENERIC seam, not the method name: a capability that writes through the
// canonical committer or the mutations dispatcher is clean even though the
// method is literally called Upsert / CommitAndIndex.
func TestScanMediaWriteBridgeBan_NarrowPortExempt(t *testing.T) {
	tmp := t.TempDir()
	writeGoFile(t, tmp, "internal/capabilities/nouveau/narrow.go", `package fixture

type AssetWriter interface {
	Upsert(ctx any, a any) error
	CommitAndIndex(ctx any, req any) error
}

func persist(w AssetWriter, ctx, a any) error {
	return w.Upsert(ctx, a)
}
`)

	if got := mediaWriteBridgeViolations(scanMediaWriteBridge(t, tmp)); len(got) != 0 {
		t.Fatalf("a narrow port is not the generic seam, got %d: %+v", len(got), got)
	}
}

// TestScanMediaWriteBridgeBan_ZoneExempt pins that the seam's own
// implementation plane is exempt by construction.
func TestScanMediaWriteBridgeBan_ZoneExempt(t *testing.T) {
	tmp := t.TempDir()
	body := detailSeamHeader + `type svc struct {
	repo detail.Repository
}

func (s *svc) Do(ctx any) error {
	return s.repo.Upsert(ctx, nil)
}
`
	for _, rel := range []string{
		"internal/kernel/asset/detail/service.go",
		"internal/platform/sqlite/assets/imagesregistry/clips_crud.go",
		"internal/platform/postgres/media/media_committer.go",
	} {
		writeGoFile(t, tmp, rel, body)
	}
	if got := mediaWriteBridgeViolations(scanMediaWriteBridge(t, tmp)); len(got) != 0 {
		t.Fatalf("the seam's implementation plane must be exempt, got %d: %+v", len(got), got)
	}
}

// TestScanMediaWriteBridgeBan_RegisterExemptByExactPath pins that the register
// is exact-file: the listed file is exempt, a NEW sibling in the same package
// is not.
//
// The production registers are EMPTY by design (see
// TestMediaWriteBridgeRegistersAreEmpty), so the mechanism is exercised on a
// synthetic entry: it must stay sound because a future documented exception
// will use exactly this path.
func TestScanMediaWriteBridgeBan_RegisterExemptByExactPath(t *testing.T) {
	const listed = "internal/capabilities/nouveau/listed.go"
	const sibling = "internal/capabilities/nouveau/brand_new.go"

	mediaWriteBridgeGrandfatheredFiles[listed] = true
	defer delete(mediaWriteBridgeGrandfatheredFiles, listed)

	tmp := t.TempDir()
	body := detailSeamHeader + `type svc struct {
	repo detail.Repository
}

func (s *svc) Do(ctx any) error {
	return s.repo.SoftDelete(ctx, "x")
}
`
	writeGoFile(t, tmp, listed, body)
	writeGoFile(t, tmp, sibling, body)

	got := mediaWriteBridgeViolations(scanMediaWriteBridge(t, tmp))
	if len(got) != 1 {
		t.Fatalf("expected exactly the unlisted sibling to be flagged, got %d: %+v", len(got), got)
	}
	if !strings.HasSuffix(got[0].File, "brand_new.go") {
		t.Errorf("flagged %q, want the unlisted sibling brand_new.go", got[0].File)
	}
}

// TestMediaWriteBridgeRegistersAreEmpty is the ACCEPTANCE pin for the
// write-bridge work: no production file, and no documented degrade path, still
// performs a media write through the generic detail.Repository/detail.Service
// seam. The gate is therefore a pure forward-prevention rule with a ZERO
// allowlist, not a ratchet that still holds debt.
//
// If this test fails, the correct response is to migrate the consumer — not to
// keep the entry. Re-adding one is a reviewed admission that a media write has
// no single owner.
func TestMediaWriteBridgeRegistersAreEmpty(t *testing.T) {
	if len(mediaWriteBridgeGrandfatheredFiles) != 0 {
		t.Errorf("mediaWriteBridgeGrandfatheredFiles must be EMPTY (zero production seam writes); got %d entr(ies): %v", len(mediaWriteBridgeGrandfatheredFiles), mediaWriteBridgeGrandfatheredFiles)
	}
	if len(mediaWriteBridgeDegradeOnlyFiles) != 0 {
		t.Errorf("mediaWriteBridgeDegradeOnlyFiles must be EMPTY (the one SQLite degrade seam was deleted with its store); got %d entr(ies): %v", len(mediaWriteBridgeDegradeOnlyFiles), mediaWriteBridgeDegradeOnlyFiles)
	}
}

// TestScanMediaWriteBridgeBan_AliasResolvedThroughImport pins that a local type
// merely spelled detail.Repository without importing the kernel detail package
// is not the seam — the gate must not be triggerable by naming convention.
func TestScanMediaWriteBridgeBan_AliasResolvedThroughImport(t *testing.T) {
	tmp := t.TempDir()
	writeGoFile(t, tmp, "internal/capabilities/nouveau/impostor.go", `package fixture

import "example.com/other/detail"

type svc struct {
	repo detail.Repository
}

func (s *svc) Do(ctx any) error {
	return s.repo.Upsert(ctx, nil)
}
`)

	if got := mediaWriteBridgeViolations(scanMediaWriteBridge(t, tmp)); len(got) != 0 {
		t.Fatalf("a same-named type from another package is not the media seam, got %d: %+v", len(got), got)
	}
}

// TestScanMediaWriteBridgeBan_TestFileExempt pins that *_test.go is out of
// scope.
func TestScanMediaWriteBridgeBan_TestFileExempt(t *testing.T) {
	tmp := t.TempDir()
	writeGoFile(t, tmp, "internal/capabilities/nouveau/writer_test.go", detailSeamHeader+`type svc struct {
	repo detail.Repository
}

func (s *svc) Do(ctx any) error {
	return s.repo.Upsert(ctx, nil)
}
`)

	if got := mediaWriteBridgeViolations(scanMediaWriteBridge(t, tmp)); len(got) != 0 {
		t.Fatalf("test files must be exempt, got %d: %+v", len(got), got)
	}
}

// TestMediaWriteBridgeRegisteredFilesExist guards the register against ghost
// entries: every listed file must still exist, so a removed consumer forces its
// entry out in the same change.
func TestMediaWriteBridgeRegisteredFilesExist(t *testing.T) {
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range allRegisteredMediaWriteBridgeFiles() {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			t.Errorf("registered media-write-bridge entry %q no longer exists: %v (delete the entry in the same change)", rel, err)
		}
	}
}

// TestMediaWriteBridgeRegisterHasNoStaleEntries is the ratchet pin. Existing is
// not enough: an entry whose seam write has been migrated must be deleted,
// otherwise the register silently becomes a permanent allowlist.
func TestMediaWriteBridgeRegisterHasNoStaleEntries(t *testing.T) {
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Skipf("repository root not resolvable from the test cwd: %v", err)
	}
	for _, rel := range allRegisteredMediaWriteBridgeFiles() {
		live, err := mediaWriteBridgeRegisterEntryIsLive(root, rel)
		if err != nil {
			t.Errorf("register entry %q is unreadable: %v", rel, err)
			continue
		}
		if !live {
			t.Errorf("register entry %q is STALE — it has no remaining generic-seam media write; delete the entry (the register must ratchet to zero, never grow into a permanent allowlist)", rel)
		}
	}
}

// TestMediaWriteBridgeRegisterEntryIsLive pins the staleness predicate itself.
func TestMediaWriteBridgeRegisterEntryIsLive(t *testing.T) {
	tmp := t.TempDir()
	writeGoFile(t, tmp, "live.go", detailSeamHeader+`type svc struct {
	repo detail.Repository
}

func (s *svc) Do(ctx any) error {
	return s.repo.SoftDelete(ctx, "x")
}
`)
	writeGoFile(t, tmp, "stale.go", detailSeamHeader+`type svc struct {
	repo detail.Repository
}

func (s *svc) Read(ctx any) {
	_, _ = s.repo.Get(ctx, "x")
}
`)
	for rel, want := range map[string]bool{"live.go": true, "stale.go": false} {
		got, err := mediaWriteBridgeRegisterEntryIsLive(tmp, rel)
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		if got != want {
			t.Errorf("%s: live = %v, want %v", rel, got, want)
		}
	}
}

// TestScanMediaWriteBridgeBan_RepoTreeIsClean is the integration pin: the real
// repository must satisfy the gate. A failure here means either a new
// production seam write landed or a registered consumer was migrated.
func TestScanMediaWriteBridgeBan_RepoTreeIsClean(t *testing.T) {
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Skipf("repository root not resolvable from the test cwd: %v", err)
	}
	got := mediaWriteBridgeViolations(scanMediaWriteBridge(t, root))
	if len(got) == 0 {
		return
	}
	var b strings.Builder
	for _, v := range got {
		b.WriteString("\n  " + v.File + ":" + itoaInt(v.Line) + " [" + v.MatchedRule + "]")
	}
	t.Fatalf("%d production media write(s) through the generic detail seam outside the register:%s", len(got), b.String())
}
