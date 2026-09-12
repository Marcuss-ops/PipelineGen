package cliprender

// content_verifier.go — clip.render's view of the shared content-verification
// authority.
//
// The memoization itself lives in internal/kernel/digest (one cache, one
// invalidation rule, shared with the Drive materializer). This file is only the
// capability-local name for it, so `cliprender.NewContentVerifier(nil)` keeps
// its historical call shape and callers never import the kernel package
// directly. There is deliberately no second implementation here: a duplicated
// memo per layer was the bug this replaced.
//
// Why the memo matters on this path: the clip.render hot path verifies
// content-addressed bytes for the source, the watermark, the background and
// every overlay segment. Those files are immutable by construction — once bytes
// exist under their content address they are never rewritten in place — so
// re-hashing the same file once per clip in a batch is pure redundant disk I/O
// (N full reads for N clips cut from one source).

import "github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"

// FileHasher computes the SHA-256 hex digest and byte size of the file at path.
// Tests inject a counting hasher to pin the "no second read" contract.
type FileHasher = digest.FileHasher

// ContentVerifier memoizes SHA-256 file verification behind a size+modtime
// validity check. A nil *ContentVerifier is safe to call: Verify falls back to
// the canonical implementation.
type ContentVerifier = digest.Verifier

// NewContentVerifier constructs the verifier. hash may be nil, in which case
// digest.SHA256File (the canonical implementation) is used.
func NewContentVerifier(hash FileHasher) *ContentVerifier {
	return digest.NewVerifier(hash)
}
