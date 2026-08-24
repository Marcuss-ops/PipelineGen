package jobs

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

// TestPayloadHash_EmptyAndWhitespaceMapToEmptyObject pins the SSOT edge case:
// worker_metrics.payloadHash must map an empty or whitespace-only payload to
// the canonical "{}" object before hashing, exactly like the canonical
// jobregistry.hashPayload. Before the guard was aligned, payloadHash("") and
// hashPayload("") produced different hashes for the same logical empty payload.
func TestPayloadHash_EmptyAndWhitespaceMapToEmptyObject(t *testing.T) {
	sum := sha256.Sum256([]byte("{}"))
	emptyObjectHash := hex.EncodeToString(sum[:])

	for _, input := range []string{"", "   ", "\n\t", "{}"} {
		if got := payloadHash(input); got != emptyObjectHash {
			t.Fatalf("payloadHash(%q) = %s, want canonical empty-object hash %s", input, got, emptyObjectHash)
		}
	}
}

// TestPayloadHash_CanonicalizesKeys guards the pre-existing canonicalization:
// equivalent JSON objects must hash identically regardless of key order.
func TestPayloadHash_CanonicalizesKeys(t *testing.T) {
	a := payloadHash(`{"video_id":"v1","n":1}`)
	b := payloadHash(`{"n":1,"video_id":"v1"}`)
	if a != b {
		t.Fatalf("payloadHash must canonicalize key order: got %s vs %s", a, b)
	}
	if a == "" || len(a) != 64 {
		t.Fatalf("payloadHash returned unexpected value %q", a)
	}
}
