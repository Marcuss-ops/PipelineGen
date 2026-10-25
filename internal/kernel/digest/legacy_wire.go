// Package digest — legacy wire compatibility for the demolished digest
// bucket.
//
// The mixed 32/64-hex bucket ("legacy_file_md5") is gone: migration 281
// promoted every valid SHA-256 into the canonical surfaces and removed the
// rest. Payloads written BEFORE that contract migration (job payloads,
// checkpoints, manifests still on disk) carry the value under the old wire
// key, so readers accept it as a ONE-WAY alias of the canonical
// "content_sha256" wire key. Nothing writes the old key any more.
package digest

import "encoding/json"

// LegacyContentSHA256Key is the demolished wire key of the legacy digest
// bucket. Read-only compatibility: never a marshal target.
const LegacyContentSHA256Key = "legacy_file_md5"

// LegacyContentSHA256FromJSON returns the digest a pre-contract payload
// carries under [LegacyContentSHA256Key], or "" when absent or unreadable.
func LegacyContentSHA256FromJSON(data []byte) string {
	var wire struct {
		Legacy string `json:"legacy_file_md5"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return ""
	}
	return wire.Legacy
}
