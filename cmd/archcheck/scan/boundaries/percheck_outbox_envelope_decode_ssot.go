package boundaries

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

const outboxEnvelopeDecodeRule = "percheck_outbox_envelope_decode_ssot"

// outboxEnvelopeExemptionStaleRule is the second half of the same gate: an
// exemption that is no longer needed must be deleted, otherwise the list grows
// into a permanent licence to duplicate the rule.
const outboxEnvelopeExemptionStaleRule = "percheck_outbox_envelope_exemption_stale"

// outboxEnvelopeDecodeExemptions lists the functions that decode an outbox
// payload WITHOUT the shared envelope decoder, with the reason each one is not
// an envelope violation.
//
// The gate targets ONE shape: a function that (a) unmarshals evt.PayloadJSON,
// (b) compares a SchemaVersion field and (c) checks an IdempotencyKey field
// inline. That is the duplicated v1-envelope prelude. A handler that delegates
// the same checks to its capability's own canonical validator (delivery,
// provider_sync, metadataexport) or that consumes a payload with no envelope at
// all (audit events, typed stage payloads, the PostgreSQL media worker's own
// plane) is NOT a violation and needs no entry here.
var outboxEnvelopeDecodeExemptions = map[string]string{
	// key: "<file>::<function>"
}

// ScanOutboxEnvelopeDecodeSSOT enforces that the v1 outbox envelope is decoded
// and validated in exactly one place (outboxevents.DecodeV1).
//
// Before this gate the same four checks — valid JSON, exact schema_version,
// non-empty event_id, non-empty idempotency_key — were written inline in six
// handlers. The policy they encode ("an envelope violation is TERMINAL, never
// retryable") is one decision, and it now has one owner.
func ScanOutboxEnvelopeDecodeSSOT(root string, _ *policy.Policy, r *report.Report) {
	seen := map[string]bool{}
	_ = filepath.WalkDir(filepath.Join(root, "internal"), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		scanOutboxEnvelopeFile(root, path, seen, r)
		return nil
	})
	reportStaleOutboxEnvelopeExemptions(seen, r)
}

func scanOutboxEnvelopeFile(root, path string, seen map[string]bool, r *report.Report) {
	rel, _ := filepath.Rel(root, path)
	rel = filepath.ToSlash(rel)
	if rel == outboxEnvelopeDecodeOwnerFile {
		return
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		// A file that cannot be parsed is already reported by the package
		// scanners; this gate stays out of the way.
		return
	}
	source, readErr := os.ReadFile(path)
	if readErr != nil {
		return
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		start := fset.Position(fn.Body.Lbrace).Offset
		end := fset.Position(fn.Body.Rbrace).Offset
		if start < 0 || end <= start || end > len(source) {
			continue
		}
		body := string(source[start:end])
		if !isInlineOutboxEnvelopePrelude(body) {
			continue
		}
		key := rel + "::" + fn.Name.Name
		seen[key] = true
		if _, exempt := outboxEnvelopeDecodeExemptions[key]; exempt {
			continue
		}
		r.Violations = append(r.Violations, report.Violation{
			File: rel, Line: fset.Position(fn.Pos()).Line,
			Rule: outboxEnvelopeDecodeRule, MatchedRule: "outbox_envelope_prelude_duplicate",
			Severity: string(report.SeverityError),
			Note: "function " + fn.Name.Name + " re-implements the v1 outbox envelope (payload parse + schema_version + idempotency_key checks). " +
				"Call outboxevents.DecodeV1 with a DecodePolicy instead: the check set and the terminal-vs-retryable decision have one owner. " +
				"A schema whose producer omits a field documents the exception with SkipIdempotencyKey/SkipEventID at the call site.",
		})
	}
}

// outboxEnvelopeDecodeOwnerFile is the ONE implementation the gate protects.
// It is excluded by path because its own bodies necessarily contain every
// signal the detector looks for.
//
// 2026-11-26: the envelope contract moved from the SQLite adapter to
// internal/kernel/outbox so a capability can consume it without importing a
// storage backend. outboxevents/envelope_decode.go is now a thin alias; the
// gate must follow the implementation, not the historical adapter path.
const outboxEnvelopeDecodeOwnerFile = "internal/kernel/outbox/envelope.go"

// envelopeSchemaComparison / envelopeIdempotencyComparison match a CHECK, not a
// mention: a handler that merely logs or forwards req.IdempotencyKey (delivery,
// clips/drive_outbox) is delegating, not duplicating.
var (
	envelopeSchemaComparison      = regexp.MustCompile(`SchemaVersion\s*[!=]=`)
	envelopeIdempotencyComparison = regexp.MustCompile(`IdempotencyKey\s*[!=]=|len\([^)]*IdempotencyKey\)\s*[!=]=`)
)

// isInlineOutboxEnvelopePrelude reports whether one function body contains the
// duplicated envelope prelude rather than delegating to the shared decoder.
//
// The three signals are required together on purpose: a handler may decode a
// payload and validate domain fields without touching the envelope, and a
// capability may own a canonical validator (assetmetadata.Validate,
// provider_sync.validate, delivery.validateDeliveryRequest) that legitimately
// checks its own schema version in one place. Only the combination "unmarshal
// the raw event payload + compare schema_version + check idempotency_key" inside
// ONE function is the duplicated prelude.
func isInlineOutboxEnvelopePrelude(body string) bool {
	if body == "" || strings.Contains(body, "DecodeV1(") {
		return false
	}
	if !strings.Contains(body, "PayloadJSON") {
		return false
	}
	if !strings.Contains(body, "json.Unmarshal") && !strings.Contains(body, "json.NewDecoder") {
		return false
	}
	return envelopeSchemaComparison.MatchString(body) && envelopeIdempotencyComparison.MatchString(body)
}

func reportStaleOutboxEnvelopeExemptions(seen map[string]bool, r *report.Report) {
	stale := make([]string, 0, len(outboxEnvelopeDecodeExemptions))
	for key := range outboxEnvelopeDecodeExemptions {
		if !seen[key] {
			stale = append(stale, key)
		}
	}
	sort.Strings(stale)
	for _, key := range stale {
		file, _, _ := strings.Cut(key, "::")
		r.Violations = append(r.Violations, report.Violation{
			File: file, Rule: outboxEnvelopeExemptionStaleRule, MatchedRule: "outbox_envelope_exemption_stale",
			Severity: string(report.SeverityError),
			Note:     "exemption " + key + " is stale: that function no longer duplicates the envelope prelude (or no longer exists). Delete the entry — " + outboxEnvelopeDecodeExemptions[key],
		})
	}
}
