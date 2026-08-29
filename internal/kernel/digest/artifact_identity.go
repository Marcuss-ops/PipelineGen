package digest

import (
	"encoding/json"
	"errors"
)

// ErrInvalidArtifactIdentity is returned when any of the canonical identity
// fields is empty or the parameters are not valid JSON.
var ErrInvalidArtifactIdentity = errors.New("digest: invalid artifact identity")

// ArtifactKeyDigest is the ONE algorithm for the deterministic identity of an
// artifact-producing computation. It is the shared SSOT behind BOTH:
//
//   - artifactcache.Key.Digest()   (the durable artifact cache address), and
//   - PreparationUnit fingerprints for artifact-producing units (result_kind
//     = ARTIFACT_CACHE),
//
// so prefetch and artifact cache share a single deterministic identity instead
// of diverging ("prefetch fingerprint A, artifact cache key B" for the same
// job). Callers must pass the canonical parameters JSON; it is re-canonicalized
// (unmarshal → marshal) so field order and whitespace cannot drift the digest.
//
// godlike/06 SSOT: the algorithm lives here, in the package both callers
// already import, never duplicated in either consumer.
func ArtifactKeyDigest(sourceSHA256, operation, paramsJSON, processorVersion string) (string, error) {
	if sourceSHA256 == "" || operation == "" || processorVersion == "" {
		return "", ErrInvalidArtifactIdentity
	}
	params := paramsJSON
	if params == "" {
		params = "{}"
	}
	var value any
	if err := json.Unmarshal([]byte(params), &value); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(struct {
		SourceSHA256     string `json:"source_sha256"`
		Operation        string `json:"operation"`
		Parameters       any    `json:"parameters"`
		ProcessorVersion string `json:"processor_version"`
	}{sourceSHA256, operation, value, processorVersion})
	if err != nil {
		return "", err
	}
	return SHA256Bytes(canonical), nil
}
