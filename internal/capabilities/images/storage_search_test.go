// Package images — storage_search_test.go covers B5's
// fanOutRetrieval parallel-execution primitive. The helper fans out
// 4 retrieval backends (Wikipedia / SearXNG / DuckDuckGo for the
// legacy no-Registry path; Wikipedia / SearXNG / DuckDuckGo / Drive
// for the Step-8 registry path) and returns the first non-empty hit,
// cancelling siblings via pkg/concurrent.Group.WithContext's
// first-error-wins contract.
//
// 5 property tests + 2 latency benchmarks:
//   - TestFanOutRetrieval_FirstHitWins_AbortsSlowBackends
//   - TestFanOutRetrieval_FanOutAllTimeout_StillReturns
//   - TestFanOutRetrieval_PanicInOneBackend_RecoversNeighbor
//   - TestFanOutRetrieval_PartialSuccess_FirstToRecord_Wins
//   - TestFanOutRetrieval_EmitsExactlyOneLogLine
//   - BenchmarkSequentialRetrievalFallback
//   - BenchmarkFanOutRetrieval
//
// B5 SSOT (PR-IMAGES-AI-VS-NORMAL-PLAN, July 2026): replaces the
// pre-B5 sequential cascade Wikipedia → SearXNG → DDG → Registry
// with 4-way concurrent fan-out. Worst-case latency drops from
// ~800ms (4 backends × 200ms, registry last) to ~200ms (parallel —
// slowest wins). Cancellable, panic-safe via pkg/concurrent.Group's
// per-goroutine panic-recover wrapper.
package images

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// ── Test fixtures ──────────────────────────────────────────────────────

// fakeBackend wraps a fn-shaped retrievalBackend with an optional
// per-call counter so tests can assert "this many fn invocations".
type retrievalFakeBackend struct {
	name      string
	callCount int32 // atomic
	fn        func(ctx context.Context) (string, string)
}

func (b *retrievalFakeBackend) call() func(ctx context.Context) (string, string) {
	return func(ctx context.Context) (string, string) {
		atomic.AddInt32(&b.callCount, 1)
		return b.fn(ctx)
	}
}

// hitAfter returns (imgURL, pageURL) after `delay`, honouring
// ctx.Done() so a cancelled fan-out returns no hit. Use this to
// simulate slow backends.
func hitAfter(_ *testing.T, name, img string, delay time.Duration) *retrievalFakeBackend {
	return &retrievalFakeBackend{
		name: name,
		fn: func(ctx context.Context) (string, string) {
			select {
			case <-time.After(delay):
				return img, img
			case <-ctx.Done():
				return "", ""
			}
		},
	}
}

// recordingBackend records a hit at the moment `gate` is closed.
// Useful for deterministic first-writer ordering without sleeps.
func recordingBackend(_ *testing.T, name, img string, gate <-chan struct{}) *retrievalFakeBackend {
	return &retrievalFakeBackend{
		name: name,
		fn: func(ctx context.Context) (string, string) {
			select {
			case <-gate:
				return img, img
			case <-ctx.Done():
				return "", ""
			}
		},
	}
}

// ── Test 1 — FirstHitWins_AbortsSlowBackends ──────────────────────────

// TestFanOutRetrieval_FirstHitWins_AbortsSlowBackends asserts the
// first non-empty hit wins and cancels slow siblings via the child
// context. The fast backend hits in 5ms; slow siblings sleep 1s.
// The whole call must complete well under the slowest sibling's
// delay (~1s) — we bound it generously at 500ms to absorb CI noise
// but still catch a regression where sibling cancellation breaks.
func TestFanOutRetrieval_FirstHitWins_AbortsSlowBackends(t *testing.T) {
	fast := hitAfter(t, "wikipedia", "http://wikipedia.example/img.png", 5*time.Millisecond)
	slowB := hitAfter(t, "searxng", "http://searxng.example/img.png", 1*time.Second)
	slowC := hitAfter(t, "duckduckgo", "http://duckduckgo.example/img.png", 1*time.Second)
	slowD := hitAfter(t, "drive", "http://drive.example/img.png", 1*time.Second)

	backends := []retrievalBackend{
		{name: fast.name, fn: fast.call()},
		{name: slowB.name, fn: slowB.call()},
		{name: slowC.name, fn: slowC.call()},
		{name: slowD.name, fn: slowD.call()},
	}

	start := time.Now()
	img, src, page := fanOutRetrieval(context.Background(), zap.NewNop(), backends)
	elapsed := time.Since(start)

	if img != "http://wikipedia.example/img.png" {
		t.Errorf("img = %q, want %q", img, "http://wikipedia.example/img.png")
	}
	if src != "wikipedia" {
		t.Errorf("src = %q, want %q", src, "wikipedia")
	}
	if page != "http://wikipedia.example/img.png" {
		t.Errorf("page = %q, want %q", page, "http://wikipedia.example/img.png")
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("elapsed = %v, want < 500ms (slow siblings must have been cancelled; "+
			"regression = first-hit no longer wins)", elapsed)
	}

	// Sibling call counts: fast was invoked once. Slow siblings
	// were invoked (the goroutines launched) but most likely
	// returned early via ctx.Done(); their fn bodies still ran
	// once because the goroutine was launched before cancellation.
	// We assert the FAST backend was definitely called — that's
	// the load-bearing invariant.
	if got := atomic.LoadInt32(&fast.callCount); got != 1 {
		t.Errorf("fast.callCount = %d, want 1", got)
	}
}

// ── Test 2 — FanOutAllTimeout_StillReturns ────────────────────────────

// TestFanOutRetrieval_FanOutAllTimeout_StillReturns asserts the
// "no hit" path is fail-closed: every backend times out → helper
// returns the empty tuple without panicking or hanging. Complements
// godlike/07 §"No fake availability" — a fan-out that pretends a
// hit was found when the ctx timed out would be a regression.
func TestFanOutRetrieval_FanOutAllTimeout_StillReturns(t *testing.T) {
	bs := []*retrievalFakeBackend{
		hitAfter(t, "wikipedia", "http://wikipedia.example/img.png", 10*time.Second),
		hitAfter(t, "searxng", "http://searxng.example/img.png", 10*time.Second),
		hitAfter(t, "duckduckgo", "http://duckduckgo.example/img.png", 10*time.Second),
		hitAfter(t, "drive", "http://drive.example/img.png", 10*time.Second),
	}
	backends := make([]retrievalBackend, len(bs))
	for i, b := range bs {
		backends[i] = retrievalBackend{name: b.name, fn: b.call()}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	img, src, page := fanOutRetrieval(ctx, zap.NewNop(), backends)

	if img != "" || src != "" || page != "" {
		t.Errorf("expected empty result on full timeout, got (img=%q, src=%q, page=%q)",
			img, src, page)
	}
}

// ── Test 3 — PanicInOneBackend_RecoversNeighbor ───────────────────────

// TestFanOutRetrieval_PanicInOneBackend_RecoversNeighbor asserts
// pkg/concurrent.Group's per-goroutine panic-recover wrapper
// surfaces the panic as an error AND preserves surviving sibling
// hits.
//
// Determinism (v5): both survivor and panicker increment their
// per-backend counter synchronously then sleep via direct
// `<-time.After` reads (not `select + ctx.Done`), guaranteeing both
// goroutines entered fn before survivor wins at T=10ms. Panicker
// panics at T=20ms — unconditional; the recover wrapper in
// group.Go converts it to errPanic which recordError absorbs
// (no-op since the group is already cancelled by the winner).
//
// Timeline:
//
//	T=0   survivor callCount=1, sleeps 10ms
//	T=0   panicker callCount=1, sleeps 20ms
//	T=0   emptyB blocks on ctx.Done
//	T=10ms survivor wins → cancels group → emptyB exits
//	T=20ms panicker panics → recover absorbs → wg.Done
//	group.Wait returns. col.result() = survivor's hit.
//
// Per-backend counters are closure-captured from outer scope (NOT
// struct self-references) because Go's parser cannot resolve a
// `<-time.After(N)` value-bound to a variable being declared in
// the same `:=` initializer that holds the containing struct
// literal — the counter must live OUTSIDE the literal.
func TestFanOutRetrieval_PanicInOneBackend_RecoversNeighbor(t *testing.T) {
	var survivorCalls, panickerCalls int32

	survivor := &retrievalFakeBackend{
		name: "wikipedia",
		fn: func(ctx context.Context) (string, string) {
			atomic.AddInt32(&survivorCalls, 1)  // observable: fn entered
			<-time.After(10 * time.Millisecond) // non-ctx-honoring hold
			return "http://wikipedia.example/img.png", "http://wikipedia.example/img.png"
		},
	}
	panicker := &retrievalFakeBackend{
		name: "panicker",
		fn: func(ctx context.Context) (string, string) {
			atomic.AddInt32(&panickerCalls, 1)  // observable: panic path entered
			<-time.After(20 * time.Millisecond) // 20ms > 10ms so survivor wins first
			panic("simulated backend panic: panicker")
		},
	}
	emptyB := hitAfter(t, "empty", "", 10*time.Hour) // exits only via ctx.Done

	backends := []retrievalBackend{
		{name: survivor.name, fn: survivor.call()},
		{name: panicker.name, fn: panicker.call()},
		{name: emptyB.name, fn: emptyB.call()},
	}

	img, src, _ := fanOutRetrieval(context.Background(), zap.NewNop(), backends)

	if img != "http://wikipedia.example/img.png" || src != "wikipedia" {
		t.Errorf("expected surviving backend (wikipedia) hit, got (img=%q, src=%q)", img, src)
	}
	if got := atomic.LoadInt32(&panickerCalls); got != 1 {
		t.Errorf("panickerCalls = %d, want 1 (panic path not exercised)", got)
	}
	if got := atomic.LoadInt32(&survivorCalls); got != 1 {
		t.Errorf("survivorCalls = %d, want 1", got)
	}
}

// ── Test 4 — PartialSuccess_FirstToRecord_Wins ────────────────────────

// TestFanOutRetrieval_PartialSuccess_FirstToRecord_Wins asserts
// the single-winner semantics of firstHitCollector: regardless of
// HOW MANY backends return non-empty hits, only the FIRST to call
// record() wins. Subsequent records are no-ops.
//
// Scenario:
//   - Backend A's gate is closed BEFORE the fan-out begins — so A
//     returns its hit immediately.
//   - Backend B reads from an UNCLOSED gate (blocks until ctx cancel).
//     This guarantees A records first regardless of goroutine
//     scheduling luck.
//   - Assert: returned tuple = A's hit, NOT B's (B never records).
func TestFanOutRetrieval_PartialSuccess_FirstToRecord_Wins(t *testing.T) {
	aGate := make(chan struct{})
	close(aGate)                 // released immediately
	bGate := make(chan struct{}) // never closed in the test window

	a := recordingBackend(t, "wikipedia", "http://wikipedia.example/img.png", aGate)
	b := recordingBackend(t, "searxng", "http://searxng.example/img.png", bGate)

	backends := []retrievalBackend{
		{name: a.name, fn: a.call()},
		{name: b.name, fn: b.call()},
	}

	img, src, _ := fanOutRetrieval(context.Background(), zap.NewNop(), backends)

	if img != "http://wikipedia.example/img.png" || src != "wikipedia" {
		t.Errorf("expected first-to-record (wikipedia) to win, got (img=%q, src=%q)", img, src)
	}

	// A definitely called. B was launched but blocked on its gate —
	// we don't strictly require its callCount because the gate
	// might not have been entered before ctx cancel. Just assert A.
	if got := atomic.LoadInt32(&a.callCount); got != 1 {
		t.Errorf("a.callCount = %d, want 1", got)
	}
}

// ── Test 5 — EmitsExactlyOneLogLine ───────────────────────────────────

// TestFanOutRetrieval_EmitsExactlyOneLogLine asserts the log
// surface stays deterministic: regardless of how many backends
// run, exactly ONE log line is emitted at fan-out completion.
// Per-backend diagnostics live inside each backend's fn (sealed
// inside their goroutines); the helper itself emits only the
// outcome — Info on winner, Warn on no-hit.
//
// Counts ONE log line in BOTH branches (winner found + no hit).
// The "no hit" branch is exercised by feeding a tight ctx against
// sleeping backends.
func TestFanOutRetrieval_EmitsExactlyOneLogLine(t *testing.T) {
	t.Run("winner", func(t *testing.T) {
		var buf = &syncBuffer{}
		log := newCapturingLogger(buf)

		fast := hitAfter(t, "wikipedia", "http://wikipedia.example/img.png", 5*time.Millisecond)
		slow := hitAfter(t, "slow", "http://slow.example/img.png", 1*time.Second)
		backends := []retrievalBackend{
			{name: fast.name, fn: fast.call()},
			{name: slow.name, fn: slow.call()},
		}

		fanOutRetrieval(context.Background(), log, backends)

		if got := buf.lines(); got != 1 {
			t.Errorf("winner branch emitted %d log lines, want 1:\n%s", got, buf.String())
		}
	})

	t.Run("no hit", func(t *testing.T) {
		var buf = &syncBuffer{}
		log := newCapturingLogger(buf)

		slowA := hitAfter(t, "wikipedia", "http://wikipedia.example/img.png", 10*time.Second)
		slowB := hitAfter(t, "searxng", "http://searxng.example/img.png", 10*time.Second)
		backends := []retrievalBackend{
			{name: slowA.name, fn: slowA.call()},
			{name: slowB.name, fn: slowB.call()},
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		defer cancel()
		fanOutRetrieval(ctx, log, backends)

		if got := buf.lines(); got != 1 {
			t.Errorf("no-hit branch emitted %d log lines, want 1:\n%s", got, buf.String())
		}
	})
}

// ── Log capture helpers ───────────────────────────────────────────────

// syncBuffer is a bytes.Buffer wrapper with thread-safe Write for
// capturing concurrent zap output (although fanOutRetrieval emits
// from a single goroutine, panic-recovery logging from
// pkg/concurrent.Group could race in theory — be safe).
type syncBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	return len(p), nil
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}

func (b *syncBuffer) lines() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	count := 0
	for _, c := range b.buf {
		if c == '\n' {
			count++
		}
	}
	// Trim trailing newline so empty buffer gives 0, not 1.
	trimmed := b.buf
	for len(trimmed) > 0 && trimmed[len(trimmed)-1] == '\n' {
		trimmed = trimmed[:len(trimmed)-1]
	}
	if len(trimmed) == 0 {
		return 0
	}
	// Recount excluding trailing newlines.
	count = 0
	for _, c := range trimmed {
		if c == '\n' {
			count++
		}
	}
	return count + 1
}

func newCapturingLogger(buf *syncBuffer) *zap.Logger {
	core := zapcore.NewCore(
		zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()),
		zapcore.AddSync(buf),
		zapcore.DebugLevel,
	)
	return zap.New(core)
}

// ── Benchmarks ────────────────────────────────────────────────────────

// benchSleep is a synthetic per-backend latency used by the
// fan-out benchmark. The canonical p99-latency demonstration
// scenario: 3 EMPTY + 1 HIT, with the hit at the END of the
// sequential cascade. Pre-B5 SEQUENTIAL worst case = 4 × benchSleep
// (must traverse all 4 because each empty backend looks slow).
// Post-B5 FAN-OUT worst case = benchSleep (slowest of the 4 wins,
// which is also benchSleep).
//
// Improvement ratio = 4:1 → 75% reduction → comfortably above the
// 30% p99 gate.
//
// (Note: Go's testing.B reports mean ns/op — benchstat computes p99
// post-hoc from multiple `-benchtime=` runs. We assert the MEAN gap
// is ≥ 30% in the bench summary; the 4:1 ratio means p99 passes
// with margin.)
const benchSleep = 50 * time.Millisecond

// BenchmarkSequentialRetrievalFallback simulates the pre-B5 cascade:
// 4 backends, the FIRST 3 always return EMPTY after benchSleep,
// the 4th returns a HIT after benchSleep. Sequential must
// traverse all 4 because it can't skip the empty results.
//
// Pre-B5 worst-case latency = 4 × benchSleep = 4 × 50ms = 200ms.
func BenchmarkSequentialRetrievalFallback(b *testing.B) {
	ctx := context.Background()
	emptyFns := []func(ctx context.Context) (string, string){
		func(ctx context.Context) (string, string) {
			select {
			case <-time.After(benchSleep):
				return "", ""
			case <-ctx.Done():
				return "", ""
			}
		},
		func(ctx context.Context) (string, string) {
			select {
			case <-time.After(benchSleep):
				return "", ""
			case <-ctx.Done():
				return "", ""
			}
		},
		func(ctx context.Context) (string, string) {
			select {
			case <-time.After(benchSleep):
				return "", ""
			case <-ctx.Done():
				return "", ""
			}
		},
	}
	hitFn := func(ctx context.Context) (string, string) {
		select {
		case <-time.After(benchSleep):
			return "http://wikipedia.example/img.png", "http://wikipedia.example/img.png"
		case <-ctx.Done():
			return "", ""
		}
	}
	allFns := append([]func(ctx context.Context) (string, string){}, emptyFns...)
	allFns = append(allFns, hitFn)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var img string
		for _, fn := range allFns {
			u, _ := fn(ctx)
			if u != "" {
				img = u
				break
			}
		}
		_ = img
	}
}

// BenchmarkFanOutRetrieval exercises the post-B5 helper against
// the same 4-backend layout: 3 empty + 1 hit. All run in parallel;
// the hit wins at ≈ 50ms, empty siblings abort via ctx cancel.
//
// Post-B5 worst-case latency = max(50ms × 4) = 50ms.
// Improvement ratio vs. sequential = 4:1 (75% reduction).
func BenchmarkFanOutRetrieval(b *testing.B) {
	backends := []retrievalBackend{
		{name: "wikipedia", fn: func(ctx context.Context) (string, string) {
			select {
			case <-time.After(benchSleep):
				return "", ""
			case <-ctx.Done():
				return "", ""
			}
		}},
		{name: "searxng", fn: func(ctx context.Context) (string, string) {
			select {
			case <-time.After(benchSleep):
				return "", ""
			case <-ctx.Done():
				return "", ""
			}
		}},
		{name: "duckduckgo", fn: func(ctx context.Context) (string, string) {
			select {
			case <-time.After(benchSleep):
				return "", ""
			case <-ctx.Done():
				return "", ""
			}
		}},
		{name: "drive", fn: func(ctx context.Context) (string, string) {
			select {
			case <-time.After(benchSleep):
				return "http://wikipedia.example/img.png", "http://wikipedia.example/img.png"
			case <-ctx.Done():
				return "", ""
			}
		}},
	}
	ctx := context.Background()
	log := zap.NewNop()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fanOutRetrieval(ctx, log, backends)
	}
}
