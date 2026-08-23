// Package qdrantid owns the pure deterministic Qdrant point-ID derivation.
package qdrantid

import (
	"encoding/hex"

	"github.com/Marcuss-ops/PipelineGen/pkg/digest"
	"github.com/google/uuid"
)

// AssetIDToQdrantPointID maps an asset ID to a deterministic UUID v8 string.
func AssetIDToQdrantPointID(assetID string) string {
	if assetID == "" {
		return ""
	}
	digestHex := digest.SHA256Bytes([]byte(assetID))
	digestBytes, err := hex.DecodeString(digestHex)
	if err != nil {
		return ""
	}
	var b [16]byte
	copy(b[:], digestBytes[:16])
	b[6] = (b[6] & 0x0f) | 0x80
	b[8] = (b[8] & 0x3f) | 0x80
	return uuid.UUID(b).String()
}

// HexDigest exposes the canonical digest for diagnostics and tests.
func HexDigest(assetID string) string {
	if assetID == "" {
		return ""
	}
	return digest.SHA256Bytes([]byte(assetID))
}
