package entitycatalog

// audit.go owns the read-only audit of the durable PERSON image catalog.
//
// The catalog can accumulate three kinds of damage that no single write can
// prevent: the same image stored twice for one person (two provider URLs that
// resolved to the same bytes, or the same URL re-inserted by an older unique
// key), a PERSON row whose name is actually a scene phrase ("Corrado Augias
// portrait"), and a materialization whose content address no longer has bytes
// in the local CAS. This file CLASSIFIES those rows; it never changes storage.
// The caller (cmd/admin images-catalog-cleanup) decides whether to act.
//
// Nothing here guesses identity: a duplicate is two rows that already agree on
// an identity fact (normalized URL or content address), and a suspect entity is
// one whose name ends with a documented media/role qualifier or a year. Merging
// two different spellings of a person is an alias decision, not a cleanup.

import (
	"fmt"
	"sort"
	"strings"
)

// Catalog audit finding kinds. Each name is a stable machine-readable label.
const (
	CatalogFindingDuplicateSourceURL   = "duplicate_source_url"
	CatalogFindingDuplicateContentSHA  = "duplicate_content_sha256"
	CatalogFindingSuspectPersonEntity  = "suspect_person_entity"
	CatalogFindingMaterializationNoCAS = "materialization_cas_missing"
)

// catalogAuditSuspectQualifierTokens are the trailing words that make a PERSON
// name a media/scene description instead of a person: "Corrado Augias
// portrait", "Documentate Overlay". The rule is closed on purpose — a token not
// listed here is left to the operator, because a false positive retires a real
// portrait.
const catalogAuditSuspectQualifierTokens = "portrait|photo|photograph|headshot|picture|closeup|close-up|selfie|avatar|thumbnail|gallery|slideshow|video|interview|poster|cover|frame|image|overlay|montage|collage|infographic|logo|chart|map"

// CatalogAuditRow is one joined candidate + entity + materialization projection.
// It is the complete input of the audit: every rule below reads only these
// fields, which keeps the classification testable without a database.
type CatalogAuditRow struct {
	CandidateID           int64
	EntityID              string
	EntityName            string
	SourceURL             string
	ContentSHA256         string
	CandidateStatus       string
	SemanticStatus        string
	MaterializationStatus string
	MaterializationSHA    string
}

// CatalogAuditOptions selects the reversible actions the audit may propose.
// The zero value reports everything and changes nothing.
type CatalogAuditOptions struct {
	// RetireDuplicates proposes retiring the surplus rows of every duplicate
	// group (all but the keeper).
	RetireDuplicates bool
	// RetireSuspectEntities proposes retiring every live candidate of an entity
	// whose name is not a person name.
	RetireSuspectEntities bool
}

// CatalogAuditFinding is one classified problem. CandidateIDs is always sorted;
// the first element of a duplicate group is the keeper.
type CatalogAuditFinding struct {
	Kind         string
	EntityID     string
	EntityName   string
	CandidateIDs []int64
	Detail       string
}

// CatalogAuditReport is the audit result.
type CatalogAuditReport struct {
	Candidates int
	Findings   []CatalogAuditFinding
	// Retire lists the candidate ids the selected options would retire. It is
	// empty unless an option is set.
	Retire []int64
}

// HasFindings reports whether the audit found anything worth reading.
func (r CatalogAuditReport) HasFindings() bool { return len(r.Findings) > 0 }

// AuditCatalog classifies the rows. casHasObject may be nil, in which case the
// CAS check is skipped — an audit must not claim a missing object it never
// looked for.
func AuditCatalog(rows []CatalogAuditRow, opts CatalogAuditOptions, casHasObject func(contentSHA256 string) bool) CatalogAuditReport {
	report := CatalogAuditReport{Candidates: len(rows)}
	retire := make(map[int64]struct{})

	// Duplicates. A retired row is already out of use, so it is never the
	// keeper and never a duplicate: counting it would make the report depend on
	// how many times the cleanup already ran.
	byURL := make(map[string][]int64)
	bySHA := make(map[string][]int64)
	for _, row := range rows {
		if isRetiredStatus(row.CandidateStatus) {
			continue
		}
		entity := strings.TrimSpace(row.EntityID)
		if entity == "" {
			continue
		}
		if url := NormalizeSourceURL(row.SourceURL); url != "" {
			key := entity + "\x00" + url
			byURL[key] = append(byURL[key], row.CandidateID)
		}
		if sha := normalizeContentAddress(row.ContentSHA256); sha != "" {
			key := entity + "\x00" + sha
			bySHA[key] = append(bySHA[key], row.CandidateID)
		}
	}
	report.Findings = append(report.Findings,
		duplicateFindings(rows, byURL, CatalogFindingDuplicateSourceURL, "identical normalized source URL", opts.RetireDuplicates, retire)...)
	report.Findings = append(report.Findings,
		duplicateFindings(rows, bySHA, CatalogFindingDuplicateContentSHA, "identical content address", opts.RetireDuplicates, retire)...)

	// Suspect PERSON entities. Grouping by entity is what makes the rule cheap
	// and the action coherent: a polluted entity is retired as a whole, never
	// half of it.
	entityIDs := make(map[string][]int64)
	for _, row := range rows {
		if isRetiredStatus(row.CandidateStatus) {
			continue
		}
		entity := strings.TrimSpace(row.EntityID)
		if entity == "" {
			continue
		}
		entityIDs[entity] = append(entityIDs[entity], row.CandidateID)
	}
	suspectEntities := make([]string, 0)
	for entity := range entityIDs {
		names := entityNames(rows, entity)
		for _, name := range names {
			if reason := suspectPersonNameReason(name); reason != "" {
				suspectEntities = append(suspectEntities, entity)
				report.Findings = append(report.Findings, CatalogAuditFinding{
					Kind:         CatalogFindingSuspectPersonEntity,
					EntityID:     entity,
					EntityName:   name,
					CandidateIDs: sortedIDs(entityIDs[entity]),
					Detail:       reason,
				})
				break
			}
		}
	}
	if opts.RetireSuspectEntities {
		for _, entity := range suspectEntities {
			for _, id := range entityIDs[entity] {
				retire[id] = struct{}{}
			}
		}
	}

	// Materializations whose bytes are not on this host. This is a report-only
	// finding: the catalog stores no path, so there is nothing to rewrite, and
	// the reuse path already falls back to the Drive copy. It is aggregated per
	// entity because a catalog populated before the local CAS existed reports
	// every historical image — one line per row would bury the actionable
	// findings (duplicates and polluted entities) under expected noise.
	if casHasObject != nil {
		missingByEntity := make(map[string][]int64)
		meta := make(map[string]string)
		for _, row := range rows {
			if isRetiredStatus(row.CandidateStatus) {
				continue
			}
			if !strings.EqualFold(strings.TrimSpace(row.MaterializationStatus), MaterializationStatusMaterialized) {
				continue
			}
			sha := normalizeContentAddress(row.MaterializationSHA)
			if sha == "" || casHasObject(sha) {
				continue
			}
			entity := strings.TrimSpace(row.EntityID)
			missingByEntity[entity] = append(missingByEntity[entity], row.CandidateID)
			if name := strings.TrimSpace(row.EntityName); name != "" {
				if _, ok := meta[entity]; !ok {
					meta[entity] = name
				}
			}
		}
		entities := make([]string, 0, len(missingByEntity))
		for entity := range missingByEntity {
			entities = append(entities, entity)
		}
		sort.Strings(entities)
		for _, entity := range entities {
			ids := sortedIDs(missingByEntity[entity])
			report.Findings = append(report.Findings, CatalogAuditFinding{
				Kind:         CatalogFindingMaterializationNoCAS,
				EntityID:     entity,
				EntityName:   meta[entity],
				CandidateIDs: ids,
				Detail:       fmt.Sprintf("%d materialized row(s) have no bytes in the local CAS; reuse falls back to the Drive copy", len(ids)),
			})
		}
	}

	sort.SliceStable(report.Findings, func(i, j int) bool {
		if report.Findings[i].Kind != report.Findings[j].Kind {
			return report.Findings[i].Kind < report.Findings[j].Kind
		}
		if report.Findings[i].EntityID != report.Findings[j].EntityID {
			return report.Findings[i].EntityID < report.Findings[j].EntityID
		}
		return firstID(report.Findings[i].CandidateIDs) < firstID(report.Findings[j].CandidateIDs)
	})
	report.Retire = make([]int64, 0, len(retire))
	for id := range retire {
		report.Retire = append(report.Retire, id)
	}
	sort.Slice(report.Retire, func(i, j int) bool { return report.Retire[i] < report.Retire[j] })
	return report
}

// duplicateFindings renders one finding per group with more than one row. The
// keeper is the lowest candidate id: candidate ids are insertion order, so the
// oldest row survives and the report is deterministic across runs.
func duplicateFindings(rows []CatalogAuditRow, groups map[string][]int64, kind, detail string, retireSurplus bool, retire map[int64]struct{}) []CatalogAuditFinding {
	names := make(map[string]string, len(rows))
	for _, row := range rows {
		if _, ok := names[row.EntityID]; !ok {
			names[row.EntityID] = strings.TrimSpace(row.EntityName)
		}
	}
	keys := make([]string, 0, len(groups))
	for key, ids := range groups {
		if len(ids) > 1 {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	out := make([]CatalogAuditFinding, 0, len(keys))
	for _, key := range keys {
		ids := sortedIDs(groups[key])
		entity := key
		if idx := strings.IndexByte(key, 0); idx >= 0 {
			entity = key[:idx]
		}
		out = append(out, CatalogAuditFinding{
			Kind:         kind,
			EntityID:     entity,
			EntityName:   names[entity],
			CandidateIDs: ids,
			Detail:       detail,
		})
		if !retireSurplus {
			continue
		}
		for _, id := range ids[1:] {
			retire[id] = struct{}{}
		}
	}
	return out
}

// suspectPersonNameReason returns why a PERSON entity name is not a person
// name, or "". The rule is deliberately closed: a trailing media/role qualifier
// or a trailing year. Everything else — including a two-word name — is left to
// the operator, because a false positive retires a real portrait.
func suspectPersonNameReason(name string) string {
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(name)))
	if len(fields) == 0 {
		return "empty PERSON name"
	}
	last := strings.Trim(fields[len(fields)-1], ".,;:!?()[]{}\"'")
	if last == "" {
		return "empty PERSON name"
	}
	if isYearToken(last) {
		return "PERSON name ends with a year: a scene phrase, not a person"
	}
	for _, token := range strings.Split(catalogAuditSuspectQualifierTokens, "|") {
		if last == token {
			return "PERSON name ends with the media qualifier " + token + ": a scene phrase, not a person"
		}
	}
	return ""
}

// isYearToken matches a bare 4-digit year in the plausible editorial range.
func isYearToken(token string) bool {
	if len(token) != 4 {
		return false
	}
	for _, r := range token {
		if r < '0' || r > '9' {
			return false
		}
	}
	return token >= "1900" && token <= "2099"
}

func entityNames(rows []CatalogAuditRow, entityID string) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, 1)
	for _, row := range rows {
		if strings.TrimSpace(row.EntityID) != entityID {
			continue
		}
		name := strings.TrimSpace(row.EntityName)
		if name == "" {
			continue
		}
		if _, ok := seen[strings.ToLower(name)]; ok {
			continue
		}
		seen[strings.ToLower(name)] = struct{}{}
		out = append(out, name)
	}
	return out
}

func sortedIDs(ids []int64) []int64 {
	out := append([]int64(nil), ids...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func firstID(ids []int64) int64 {
	if len(ids) == 0 {
		return 0
	}
	return ids[0]
}

func isRetiredStatus(status string) bool {
	return strings.EqualFold(strings.TrimSpace(status), CandidateStatusRetired)
}

// normalizeContentAddress keeps the audit honest about what a content address
// is: a 64-hex digest. Short placeholder values are absence, not identity, and
// grouping on one would merge unrelated rows.
func normalizeContentAddress(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) != 64 {
		return ""
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return ""
		}
	}
	return value
}
