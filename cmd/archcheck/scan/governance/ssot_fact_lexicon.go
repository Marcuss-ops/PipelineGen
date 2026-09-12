// Package scan — ssot_fact_lexicon.go: the hardcoded-lexicon fact.
//
// Split out of ssot_facts.go to keep that file under the 600-LOC strict cap
// (max_lines_per_file_strict); the fact still owns exactly what the file
// header there describes: its rule-note, its detection regexes, its canonical
// owner and its stateful per-file scanner.
package governance

import (
	"bufio"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// ── Hardcoded lexicon mirrors (stop-word maps) ───────────────────────────
//
// Stop-word (and, more generally, lexicon) sets MUST be loaded from the
// LexiconRegistry (internal/capabilities/linguistics/) at bootstrap, whose
// data comes from config/lexicons/**/*.txt. Any production file that defines
// a literal stop-word map is a godlike/06 SSOT violation — stop words are
// linguistic data, not code. The gate is SCOPED to internal/ and exempts the
// canonical lexicon home (internal/capabilities/linguistics/).
//
// Known, deferred mirrors of the lexicon data are NOT silenced by a path list
// here: the file must carry a `LEXICON_MIRROR_DEBT: owner=…, deadline=…`
// marker, which keeps the debt visible as a residue warning and turns back
// into a violation on the deadline. See stopwordMapDebtMarker.
const stopwordMapNote = "forbidden hardcoded stop-word map in production code. Stop-word sets MUST be loaded from the LexiconRegistry (internal/capabilities/linguistics/) at bootstrap via linguistics.DefaultLexicon().StopWords(); the linguistic data lives in config/lexicons/<lang>/stopwords.txt. Hardcoded linguistic maps are a godlike/06 SSOT violation. See internal/capabilities/linguistics/lexicon_registry.go for the canonical approach."

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

// stopwordMapDebtMarker is the in-file allowlist for a hardcoded linguistic
// map that is KNOWN debt and cannot be migrated inside the current change.
// The marker is deliberately self-describing and deadline-bearing:
//
//	// LEXICON_MIRROR_DEBT: owner=<team>, deadline=YYYY-MM-DD — <reason>
//
// A marked file keeps the debt VISIBLE — the scanner reports it as a non-fatal
// residue warning on every run (godlike/07 residue accounting) — instead of
// silencing it through a path list in this scanner. That distinction is the
// whole point: the debt fact lives at the site, with its owner and its expiry,
// exactly once. The exemption re-arms automatically: from the day the deadline
// passes the same maps are violations again, so a marker can never authorise
// permanent debt (godlike/08 zero-baseline). A marker without a valid deadline
// is itself a violation.
const stopwordMapDebtMarker = "LEXICON_MIRROR_DEBT:"

// stopwordMapDebtDeadlineRE extracts the expiry from the marker line.
var stopwordMapDebtDeadlineRE = regexp.MustCompile(`deadline=(\d{4}-\d{2}-\d{2})`)

// stopwordMapDebt recognises the in-file debt marker. found reports whether the
// file carries it; deadline is the zero time when the marker omits or mangles
// the date (which the scanner then treats as a violation, not as an exemption).
func stopwordMapDebt(raw string) (found bool, deadline time.Time) {
	idx := strings.Index(raw, stopwordMapDebtMarker)
	if idx < 0 {
		return false, time.Time{}
	}
	line := raw[idx:]
	if nl := strings.IndexAny(line, "\r\n"); nl >= 0 {
		line = line[:nl]
	}
	m := stopwordMapDebtDeadlineRE.FindStringSubmatch(line)
	if m == nil {
		return true, time.Time{}
	}
	parsed, err := time.Parse("2006-01-02", m[1])
	if err != nil {
		return true, time.Time{}
	}
	return true, parsed
}

// scanStopwordMapRuleFile is the stateful stopword-map detector: it tracks the
// brace depth of an opened map literal and emits AT MOST ONE violation per
// map, anchored at the opener line, with the offending stop-word line as the
// snippet. In a file carrying the LEXICON_MIRROR_DEBT marker (owner + future
// deadline) the same maps are RESIDUE: they are reported as warnings and the
// gate fails closed the moment the deadline passes.
func scanStopwordMapRuleFile(_ any, path, relPath string, r *report.Report, rule *ssotRule) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	deferred, debtDeadline := stopwordMapDebt(string(raw))

	// report is the single emission point: with a live debt marker the hit is
	// counted as residue and surfaced once per file at the end of the walk.
	residue := 0
	report := func(lineNo int, snippet string) {
		if deferred {
			residue++
			return
		}
		ssotEmit(r, rule, relPath, lineNo, rule.MatchedRule,
			stopwordMapNote+" | snippet: "+truncateSSOTSnippet(snippet))
	}

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
				report(lineNo, line)
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
			report(mapOpenLine, line)
			reported = true
		}
		braceDepth += strings.Count(line, "{") - strings.Count(line, "}")
		if braceDepth <= 0 {
			inMap = false
		}
	}

	if !deferred || residue == 0 {
		return
	}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	switch {
	case debtDeadline.IsZero():
		ssotEmit(r, rule, relPath, 1, rule.MatchedRule,
			stopwordMapNote+" | the "+stopwordMapDebtMarker+" marker in this file has no valid `deadline=YYYY-MM-DD`, so it exempts nothing: add the owner and an expiry, or migrate the map.")
	case debtDeadline.Before(today):
		ssotEmit(r, rule, relPath, 1, rule.MatchedRule,
			stopwordMapNote+" | the "+stopwordMapDebtMarker+" deferral in this file expired on "+debtDeadline.Format("2006-01-02")+": migrate the "+strconv.Itoa(residue)+" hardcoded linguistic map(s) to the LexiconRegistry, or re-baseline with a new owner + deadline.")
	default:
		ssotWarn(r, rule, "lexicon-mirror-debt:", strconv.Itoa(residue)+" hardcoded linguistic map(s) in "+relPath+" deferred by "+stopwordMapDebtMarker+" until "+debtDeadline.Format("2006-01-02")+" (migrate to the LexiconRegistry / config/lexicons/** before that date)")
	}
}
