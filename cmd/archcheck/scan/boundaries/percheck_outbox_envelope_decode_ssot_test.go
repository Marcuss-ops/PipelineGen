package boundaries

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

func writeScanFixture(t *testing.T, root, rel, source string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestScanOutboxEnvelopeDecodeSSOTRejectsInlinePrelude(t *testing.T) {
	root := t.TempDir()
	writeScanFixture(t, root, "internal/capabilities/jobs/bad.go", `package jobs
func (h *H) Handle(ctx context.Context, evt outboxevents.Event) error {
	var req payload
	if err := json.Unmarshal([]byte(evt.PayloadJSON), &req); err != nil {
		return err
	}
	if req.SchemaVersion != SchemaVersion {
		return err
	}
	if req.IdempotencyKey == "" {
		return err
	}
	return nil
}
`)
	restore := swapOutboxEnvelopeExemptions(nil)
	defer restore()

	var r report.Report
	ScanOutboxEnvelopeDecodeSSOT(root, nil, &r)
	if len(r.Violations) != 1 {
		t.Fatalf("expected one violation, got %#v", r.Violations)
	}
	if r.Violations[0].MatchedRule != "outbox_envelope_prelude_duplicate" {
		t.Fatalf("unexpected matched rule %q", r.Violations[0].MatchedRule)
	}
}

func TestScanOutboxEnvelopeDecodeSSOTAcceptsSharedDecoderAndDelegatedValidator(t *testing.T) {
	root := t.TempDir()
	writeScanFixture(t, root, "internal/capabilities/jobs/decoded.go", `package jobs
func (h *H) Handle(ctx context.Context, evt outboxevents.Event) error {
	var req payload
	if err := outboxevents.DecodeV1(policy, evt, &req); err != nil {
		return err
	}
	return nil
}
`)
	writeScanFixture(t, root, "internal/capabilities/jobs/delegated.go", `package jobs
func (h *H) Handle(ctx context.Context, evt outboxevents.Event) error {
	var req payload
	if err := json.Unmarshal([]byte(evt.PayloadJSON), &req); err != nil {
		return err
	}
	return validateRequest(&req)
}
func validateRequest(req *payload) error {
	if req.SchemaVersion != SchemaVersion {
		return nil
	}
	if req.IdempotencyKey == "" {
		return nil
	}
	return nil
}
`)
	restore := swapOutboxEnvelopeExemptions(nil)
	defer restore()

	var r report.Report
	ScanOutboxEnvelopeDecodeSSOT(root, nil, &r)
	if len(r.Violations) != 0 {
		t.Fatalf("expected no violations, got %#v", r.Violations)
	}
}

func TestScanOutboxEnvelopeDecodeSSOTRejectsStaleExemption(t *testing.T) {
	root := t.TempDir()
	writeScanFixture(t, root, "internal/capabilities/jobs/decoded.go", `package jobs
func (h *H) Handle(ctx context.Context, evt outboxevents.Event) error {
	return outboxevents.DecodeV1(policy, evt, nil)
}
`)
	restore := swapOutboxEnvelopeExemptions(map[string]string{
		"internal/capabilities/jobs/decoded.go::Handle": "owner=test; removal_trigger=never",
	})
	defer restore()

	var r report.Report
	ScanOutboxEnvelopeDecodeSSOT(root, nil, &r)
	if len(r.Violations) != 1 {
		t.Fatalf("expected one stale-exemption violation, got %#v", r.Violations)
	}
	if r.Violations[0].Rule != outboxEnvelopeExemptionStaleRule {
		t.Fatalf("unexpected rule %q", r.Violations[0].Rule)
	}
}

func TestScanOutboxEnvelopeDecodeSSOTIgnoresTestFiles(t *testing.T) {
	root := t.TempDir()
	writeScanFixture(t, root, "internal/capabilities/jobs/bad_test.go", `package jobs
func (h *H) Handle(ctx context.Context, evt outboxevents.Event) error {
	var req payload
	if err := json.Unmarshal([]byte(evt.PayloadJSON), &req); err != nil {
		return err
	}
	if req.SchemaVersion != SchemaVersion || req.IdempotencyKey == "" {
		return err
	}
	return nil
}
`)
	restore := swapOutboxEnvelopeExemptions(nil)
	defer restore()

	var r report.Report
	ScanOutboxEnvelopeDecodeSSOT(root, nil, &r)
	if len(r.Violations) != 0 {
		t.Fatalf("test files are out of scope, got %#v", r.Violations)
	}
}

func swapOutboxEnvelopeExemptions(next map[string]string) func() {
	previous := outboxEnvelopeDecodeExemptions
	outboxEnvelopeDecodeExemptions = next
	return func() { outboxEnvelopeDecodeExemptions = previous }
}
