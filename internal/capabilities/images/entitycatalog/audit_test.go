package entitycatalog

import (
	"strconv"
	"strings"
	"testing"
)

const (
	auditSHAOne = "1111111111111111111111111111111111111111111111111111111111111111"
	auditSHATwo = "2222222222222222222222222222222222222222222222222222222222222222"
)

func auditRow(id int64, entity, name, url, sha string) CatalogAuditRow {
	return CatalogAuditRow{
		CandidateID:     id,
		EntityID:        entity,
		EntityName:      name,
		SourceURL:       url,
		ContentSHA256:   sha,
		CandidateStatus: CandidateStatusActive,
	}
}

// TestAuditCatalogFindsDuplicatesButKeepsTheOldest pins the duplicate rule and
// the keeper: surplus rows are reported, the lowest candidate id survives, and
// a dry run retires nothing.
func TestAuditCatalogFindsDuplicatesButKeepsTheOldest(t *testing.T) {
	rows := []CatalogAuditRow{
		auditRow(7, "person:corrado-augias", "Corrado Augias", "https://img.example/augias.jpg", auditSHAOne),
		auditRow(3, "person:corrado-augias", "Corrado Augias", "https://IMG.example/augias.jpg?utm_source=x", auditSHAOne),
		auditRow(9, "person:corrado-augias", "Corrado Augias", "https://img.example/other.jpg", auditSHAOne),
		auditRow(11, "person:corrado-augias", "Corrado Augias", "https://img.example/third.jpg", auditSHATwo),
	}

	dry := AuditCatalog(rows, CatalogAuditOptions{}, nil)
	if len(dry.Retire) != 0 {
		t.Fatalf("a dry run must propose no retirement, got %v", dry.Retire)
	}
	// Same URL twice (3 and 7) and same content address three times (3, 7, 9).
	kinds := map[string]int{}
	for _, finding := range dry.Findings {
		kinds[finding.Kind]++
		if finding.Kind == CatalogFindingDuplicateSourceURL {
			if got, want := strings.Join(idsString(finding.CandidateIDs), ","), "3,7"; got != want {
				t.Fatalf("source-url duplicate group = %s, want %s", got, want)
			}
		}
		if finding.Kind == CatalogFindingDuplicateContentSHA {
			if got, want := strings.Join(idsString(finding.CandidateIDs), ","), "3,7,9"; got != want {
				t.Fatalf("content duplicate group = %s, want %s", got, want)
			}
		}
	}
	if kinds[CatalogFindingDuplicateSourceURL] != 1 || kinds[CatalogFindingDuplicateContentSHA] != 1 {
		t.Fatalf("unexpected duplicate findings: %+v", kinds)
	}

	applied := AuditCatalog(rows, CatalogAuditOptions{RetireDuplicates: true}, nil)
	// 7 and 9 are surplus everywhere; 3 and 11 are keepers.
	if got, want := strings.Join(idsString(applied.Retire), ","), "7,9"; got != want {
		t.Fatalf("retire set = %s, want %s", got, want)
	}
}

// TestAuditCatalogIgnoresRetiredRowsOnRerun proves the audit is idempotent: a
// row the previous run retired neither keeps a duplicate group alive nor gets
// retired twice.
func TestAuditCatalogIgnoresRetiredRowsOnRerun(t *testing.T) {
	rows := []CatalogAuditRow{
		auditRow(3, "person:corrado-augias", "Corrado Augias", "https://img.example/augias.jpg", auditSHAOne),
		auditRow(7, "person:corrado-augias", "Corrado Augias", "https://img.example/augias.jpg", auditSHAOne),
	}
	rows[1].CandidateStatus = CandidateStatusRetired

	report := AuditCatalog(rows, CatalogAuditOptions{RetireDuplicates: true}, nil)
	if report.HasFindings() {
		t.Fatalf("a retired surplus row must not keep a duplicate group alive: %+v", report.Findings)
	}
	if len(report.Retire) != 0 {
		t.Fatalf("nothing left to retire, got %v", report.Retire)
	}
}

// TestAuditCatalogFlagsScenePhrasesAsSuspect pins the conservative name rule:
// a trailing media qualifier or a trailing year is a scene phrase, while real
// names — including a stage name that starts with a number — are left alone.
func TestAuditCatalogFlagsScenePhrasesAsSuspect(t *testing.T) {
	cases := []struct {
		name    string
		suspect bool
	}{
		{"Corrado Augias", false},
		{"Corrado Augias portrait", true},
		{"Corrado Augias Photo", true},
		{"Tripoli bombing 1986", true},
		{"50 Cent", false},
		{"Michael B. Jordan", false},
		{"Via Teulada interview", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows := []CatalogAuditRow{
				auditRow(1, "person:subject", tc.name, "https://img.example/x.jpg", ""),
			}
			report := AuditCatalog(rows, CatalogAuditOptions{}, nil)
			if got := report.HasFindings(); got != tc.suspect {
				t.Fatalf("suspect(%q) = %v, want %v (%+v)", tc.name, got, tc.suspect, report.Findings)
			}
		})
	}
}

// TestAuditCatalogRetiresSuspectEntityCandidatesOnlyWhenAsked proves the action
// is opt-in and covers the whole entity.
func TestAuditCatalogRetiresSuspectEntityCandidatesOnlyWhenAsked(t *testing.T) {
	rows := []CatalogAuditRow{
		auditRow(1, "person:corrado-augias-portrait", "Corrado Augias portrait", "https://img.example/1.jpg", ""),
		auditRow(2, "person:corrado-augias-portrait", "Corrado Augias portrait", "https://img.example/2.jpg", ""),
		auditRow(3, "person:corrado-augias", "Corrado Augias", "https://img.example/3.jpg", ""),
	}

	if got := AuditCatalog(rows, CatalogAuditOptions{}, nil).Retire; len(got) != 0 {
		t.Fatalf("suspect retirement must be opt-in, got %v", got)
	}
	report := AuditCatalog(rows, CatalogAuditOptions{RetireSuspectEntities: true}, nil)
	if got, want := strings.Join(idsString(report.Retire), ","), "1,2"; got != want {
		t.Fatalf("retire set = %s, want %s (the real person's row must survive)", got, want)
	}
}

// TestAuditCatalogReportsMaterializationsWithoutCASBytes proves the CAS check
// reads the store instead of assuming, and that a nil store never invents a
// finding.
func TestAuditCatalogReportsMaterializationsWithoutCASBytes(t *testing.T) {
	rows := []CatalogAuditRow{
		{
			CandidateID: 1, EntityID: "person:corrado-augias", EntityName: "Corrado Augias",
			CandidateStatus: CandidateStatusActive, MaterializationStatus: MaterializationStatusMaterialized,
			MaterializationSHA: auditSHAOne,
		},
		{
			CandidateID: 2, EntityID: "person:corrado-augias", EntityName: "Corrado Augias",
			CandidateStatus: CandidateStatusActive, MaterializationStatus: MaterializationStatusMaterialized,
			MaterializationSHA: auditSHATwo,
		},
	}

	if report := AuditCatalog(rows, CatalogAuditOptions{}, nil); report.HasFindings() {
		t.Fatalf("a nil CAS must not produce findings: %+v", report.Findings)
	}
	report := AuditCatalog(rows, CatalogAuditOptions{}, func(sha string) bool { return sha == auditSHATwo })
	if len(report.Findings) != 1 || report.Findings[0].Kind != CatalogFindingMaterializationNoCAS {
		t.Fatalf("want exactly one per-entity CAS-missing finding, got %+v", report.Findings)
	}
	if got, want := strings.Join(idsString(report.Findings[0].CandidateIDs), ","), "1"; got != want {
		t.Fatalf("CAS-missing finding must name candidate 1, got %s", got)
	}
	if len(report.Retire) != 0 {
		t.Fatalf("a CAS report must not retire rows, got %v", report.Retire)
	}
}

func idsString(ids []int64) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, strconv.FormatInt(id, 10))
	}
	return out
}

// TestAuditCatalogAggregatesCASMissingPerEntity pins the shape an operator
// reads: a catalog populated before the local CAS existed must not emit one
// line per row.
func TestAuditCatalogAggregatesCASMissingPerEntity(t *testing.T) {
	materialized := func(id int64, entity, name, sha string) CatalogAuditRow {
		return CatalogAuditRow{
			CandidateID: id, EntityID: entity, EntityName: name,
			CandidateStatus: CandidateStatusActive, MaterializationStatus: MaterializationStatusMaterialized,
			MaterializationSHA: sha,
		}
	}
	rows := []CatalogAuditRow{
		materialized(1, "person:corrado-augias", "Corrado Augias", auditSHAOne),
		materialized(2, "person:corrado-augias", "Corrado Augias", auditSHATwo),
		materialized(3, "person:vito-mancuso", "Vito Mancuso", auditSHAOne),
	}
	report := AuditCatalog(rows, CatalogAuditOptions{}, func(string) bool { return false })
	if len(report.Findings) != 2 {
		t.Fatalf("want one finding per entity, got %+v", report.Findings)
	}
	if got, want := strings.Join(idsString(report.Findings[0].CandidateIDs), ","), "1,2"; got != want {
		t.Fatalf("first entity ids = %s, want %s", got, want)
	}
	if report.Findings[1].CandidateIDs[0] != 3 {
		t.Fatalf("second entity ids = %v", report.Findings[1].CandidateIDs)
	}
}
