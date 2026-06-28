// point_types.go — Point struct (PR3 split).
//
// PR3 mechanical split (June 2026): relocated from types.go without
// signature or behaviour changes. Only the Point struct lives here.
// The companion PointPayload (used by Client.OverwritePayload and
// reaper.Reaper.RedactPayload — distinct shape, no vectors, only
// payload fields) stays in types_dr.go since QDRANT-005C PR3 already
// placed it there with a comment explaining why it's infra-only (the
// reaper fork was the canonical reason).
//
// Point is the canonical upsert shape. The Vectors field uses the
// Qdrant REST API key "vector" (singular) — this is the WIRE shape;
// internally callers use map[string]interface{} so any channel
// (text / transcript / visual / audio) can be addressed without a
// distinct Go type per EmbeddingSpec.
package qdrant

// Point is a single Qdrant point ready for upsert.
// Note: the Vectors field uses the Qdrant REST API key "vector" (singular).
type Point struct {
	ID      string                 `json:"id"`
	Vectors map[string]interface{} `json:"vector"`
	Payload map[string]interface{} `json:"payload"`
}
