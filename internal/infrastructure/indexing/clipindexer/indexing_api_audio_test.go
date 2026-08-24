// Package clipindexer — Phase 4 audio embedding TDD tests
// (PR-AUDIO-CHANNEL-EXTENSION, July 2026).
//
// indexing_api_audio_test.go pins the canonical fail-soft contract for
// the CLAP-HTSAT audio channel added in Phase 4. The 4 tests below
// cover the 4 canonical paths:
//
//  1. Happy path: sidecar returns 200 + 512d vector →
//     media_assets.audio_embedding is populated with the JSON-encoded
//     embedding. Qdrant payload_mapper.IndexDocumentToPoint picks it up
//     on the next upsert (case ChannelAudio).
//  2. HTTP 501: CLAP model not loaded on the sidecar (the canonical
//     "model unavailable" sentinel per scripts/services/embedding_server
//     /audio.py). Fail-soft: log INFO, audio_embedding stays empty,
//     IndexClip returns nil (the clip remains valid for the other 3
//     channels).
//  3. HTTP 410: endpoint retired (QDRANT-001 retirement contract).
//     Fail-soft: log INFO, audio_embedding stays empty, IndexClip
//     returns nil.
//  4. Empty local_path: Phase 4 is skipped (no audio source on disk).
//     Fail-soft: audio_embedding stays empty, IndexClip returns nil.
//
// Per godlike/07 no-fake-availability, NONE of the fail-soft paths
// must surface a typed error to the caller — the audio channel is
// OPTIONAL in the Qdrant v3 schema (case ChannelAudio in
// payload_mapper.go::IndexDocumentToPoint drops on nil). The clip
// is valid as long as text+transcript+visual are populated; audio
// is an additive search-quality boost.
package clipindexer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	drive "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
)

// phase4TestSchema inlines the MINIMUM columns the indexer writers
// + Phase 4 audio path need. The migration chain is the sole schema authority.
// here because that constant was authored before migration 094
// (QDRANT-002 PR6, June 2026) added the index_state /
// index_state_updated_at / source_version columns; the canonical
// constant has not been folded to mirror those additions (godlike/06
// SSOT drift, tracked separately — see architecture/current.yaml
// forward-pointer CANONICAL-DRIFT-MIG094). service_test.go in the
// same package uses the same inline-schema pattern; this test
// matches it. The forward-pointer to the canonical-drift fixup is
// captured in architecture/current.yaml under PR-AUDIO-CHANNEL-EXTENSION.
const phase4TestSchema = `
CREATE TABLE media_assets (
	id TEXT PRIMARY KEY,
	name TEXT,
	source TEXT,
	media_type TEXT,
	tags TEXT,
	embedding_json TEXT,
	transcript_embedding TEXT,
	visual_embedding TEXT,
	audio_embedding TEXT,
	metadata_json TEXT,
	local_path TEXT,
	search_text TEXT,
	index_state TEXT NOT NULL DEFAULT 'DISCOVERED',
	index_state_updated_at TEXT NOT NULL DEFAULT '',
	source_version TEXT NOT NULL DEFAULT '',
	file_hash TEXT NOT NULL DEFAULT '',
	language TEXT,
	workspace_id TEXT,
	lifecycle_state TEXT NOT NULL DEFAULT 'ACTIVE',
	created_at TEXT NOT NULL DEFAULT '',
	updated_at TEXT NOT NULL DEFAULT ''
)
`

// newPhase4TestService builds a clipindexer.Service with the in-memory
// SQLite schema. The mock HTTP server is wired by the individual tests
// via svc.cfg.ServerURL = server.URL.
func newPhase4TestService(t *testing.T) (*Service, *drive.SQLiteDB, func()) {
	t.Helper()
	db := drive.NewTestDBWithSchema(t, phase4TestSchema)
	svc := NewService(
		&Config{
			Enabled:    true,
			PythonBin:  "python-invalid-should-not-be-called",
			ScriptPath: "scripts/bridges/index_clips.py",
		},
		&drive.SQLiteDB{DB: db},
		":memory:",
		zap.NewNop(),
	)
	svc.vectorStore = &mockVectorStoreIndexer{} // BLOCKER #3: UpsertVectorStore now fail-closed on nil
	cleanup := func() { db.Close() }
	return svc, &drive.SQLiteDB{DB: db}, cleanup
}

// makeCLAPVector returns a 512-dim CLAP-shaped vector (alternating
// 0.1/0.05). The exact values are not load-bearing — the test only
// checks that the embedding is persisted to the right column with
// the right length.
func makeCLAPVector() []float64 {
	out := make([]float64, 512)
	for i := range out {
		if i%2 == 0 {
			out[i] = 0.1
		} else {
			out[i] = 0.05
		}
	}
	return out
}

// readAudioEmbedding reads the JSON-encoded embedding vector from
// media_assets.audio_embedding for clipID. Returns nil if the column
// is empty (the fail-soft contract paths leave it empty).
func readAudioEmbedding(t *testing.T, db *drive.SQLiteDB, clipID string) []float64 {
	t.Helper()
	var raw string
	err := db.QueryRow(`SELECT COALESCE(audio_embedding, '') FROM media_assets WHERE id = ?`, clipID).Scan(&raw)
	require.NoError(t, err)
	if raw == "" || raw == "[]" {
		return nil
	}
	var out []float64
	require.NoError(t, json.Unmarshal([]byte(raw), &out))
	return out
}

// preSeedSourceVersion computes the deterministic content hash for clipID
// and updates the row's file_hash so the CAS fence in setIndexedAt
// (BLOCKER #2) passes. Without this, a test row with file_hash=”
// would trigger ErrIndexSuperseded on every IndexClip.
func preSeedSourceVersion(t *testing.T, svc *Service, clipID string) {
	t.Helper()
	ch, _, err := svc.computeContentHash(context.Background(), clipID)
	require.NoError(t, err)
	if ch == "" {
		return
	}
	_, err = svc.db.ExecContext(context.Background(),
		`UPDATE media_assets SET file_hash = ? WHERE id = ?`, ch, clipID)
	require.NoError(t, err)
}

// TestIndexClip_PopulatesAudioChannel — happy path. Sidecar returns
// HTTP 200 with a 512d CLAP vector. After IndexClip returns, the
// audio_embedding column is populated with the JSON-encoded vector
// and the embedding length matches the sidecar's response.
func TestIndexClip_PopulatesAudioChannel(t *testing.T) {
	svc, db, cleanup := newPhase4TestService(t)
	defer cleanup()

	const clipID = "clip-audio-happy"
	_, err := db.Exec(`
INSERT INTO media_assets (id, name, source, media_type, local_path, search_text, metadata_json)
VALUES (?, 'Test Audio Clip', 'youtube', 'video', '/data/audio_happy.mp4', 'ambient rain with distant thunder', '{}')
`, clipID)
	require.NoError(t, err)
	preSeedSourceVersion(t, svc, clipID) // BLOCKER #2: CAS fence needs matching file_hash

	expectedVec := makeCLAPVector()
	var audioCalled int32
	var audioClipIDFromBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/index":
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]any{
				"clip_id":    clipID,
				"embedding":  []float64{0.1, 0.2, 0.3},
				"dimensions": 3,
			})
		case "/embed_audio_from_file":
			atomic.AddInt32(&audioCalled, 1)
			// Decode the body to confirm clip_id is in the JSON body,
			// not in a custom X-Clip-Id header (per the canonical
			// wire contract: clip_id is always in the payload).
			var payload map[string]any
			if decErr := json.NewDecoder(r.Body).Decode(&payload); decErr == nil {
				if id, ok := payload["clip_id"].(string); ok {
					audioClipIDFromBody = id
				}
			}
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]any{
				"clip_id":       clipID,
				"embedding":     expectedVec,
				"dimensions":    512,
				"model":         "clap-htsat-fused",
				"model_version": "2026-06-16-v1",
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	svc.cfg.ServerURL = server.URL

	err = svc.IndexClip(context.Background(), clipID)
	require.NoError(t, err, "Phase 4 happy path must not fail the whole IndexClip")

	assert.Equal(t, int32(1), atomic.LoadInt32(&audioCalled),
		"Phase 4 must hit /embed_audio_from_file exactly once")
	assert.Equal(t, clipID, audioClipIDFromBody,
		"Phase 4 must include clip_id in the JSON body payload")

	persisted := readAudioEmbedding(t, db, clipID)
	require.NotNil(t, persisted, "audio_embedding must be populated after happy-path Phase 4")
	assert.Equal(t, 512, len(persisted), "CLAP vector must be 512-dim")
	assert.InDelta(t, expectedVec[0], persisted[0], 1e-9, "first dimension must match the sidecar response")
}

// TestIndexClip_AudioChannelUnavailableFailSoft — HTTP 501 fail-soft.
// The sidecar's CLAP model is not loaded (canonical "model unavailable"
// sentinel per scripts/services/embedding_server/audio.py). The
// indexer must log INFO, leave audio_embedding empty, and return nil.
func TestIndexClip_AudioChannelUnavailableFailSoft(t *testing.T) {
	svc, db, cleanup := newPhase4TestService(t)
	defer cleanup()

	const clipID = "clip-audio-501"
	_, err := db.Exec(`
INSERT INTO media_assets (id, name, source, media_type, local_path, search_text, metadata_json)
VALUES (?, 'Test Audio 501', 'youtube', 'video', '/data/audio_501.mp4', 'ambient music with string section', '{}')
`, clipID)
	require.NoError(t, err)
	preSeedSourceVersion(t, svc, clipID)

	var audioCalled int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/index":
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]any{
				"clip_id":   clipID,
				"embedding": []float64{0.1, 0.2, 0.3},
			})
		case "/embed_audio_from_file":
			atomic.AddInt32(&audioCalled, 1)
			w.WriteHeader(http.StatusNotImplemented) // 501 = CLAP model not loaded
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	svc.cfg.ServerURL = server.URL

	err = svc.IndexClip(context.Background(), clipID)
	require.NoError(t, err, "HTTP 501 must NOT fail IndexClip (godlike/07 fail-soft contract)")

	assert.Equal(t, int32(1), atomic.LoadInt32(&audioCalled),
		"Phase 4 must attempt /embed_audio_from_file exactly once before failing-soft")

	persisted := readAudioEmbedding(t, db, clipID)
	assert.Nil(t, persisted,
		"audio_embedding must stay empty when sidecar returns 501 (fail-soft contract)")
}

// TestIndexClip_AudioChannelEndpointRetired — HTTP 410 fail-soft.
// The /embed_audio_from_file endpoint has been retired per the
// QDRANT-001 contract (mirroring the /index retirement pattern).
// The indexer must log INFO, leave audio_embedding empty, return nil.
func TestIndexClip_AudioChannelEndpointRetired(t *testing.T) {
	svc, db, cleanup := newPhase4TestService(t)
	defer cleanup()

	const clipID = "clip-audio-410"
	_, err := db.Exec(`
INSERT INTO media_assets (id, name, source, media_type, local_path, search_text, metadata_json)
VALUES (?, 'Test Audio 410', 'youtube', 'video', '/data/audio_410.mp4', 'dramatic music with brass section', '{}')
`, clipID)
	require.NoError(t, err)
	preSeedSourceVersion(t, svc, clipID)

	var audioCalled int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/index":
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]any{
				"clip_id":   clipID,
				"embedding": []float64{0.1, 0.2, 0.3},
			})
		case "/embed_audio_from_file":
			atomic.AddInt32(&audioCalled, 1)
			w.WriteHeader(http.StatusGone) // 410 = endpoint retired
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	svc.cfg.ServerURL = server.URL

	err = svc.IndexClip(context.Background(), clipID)
	require.NoError(t, err, "HTTP 410 must NOT fail IndexClip (QDRANT-001 retirement contract)")

	assert.Equal(t, int32(1), atomic.LoadInt32(&audioCalled))
	persisted := readAudioEmbedding(t, db, clipID)
	assert.Nil(t, persisted, "audio_embedding must stay empty when endpoint is retired")
}

// TestIndexClip_AudioChannelLocalPathEmpty — no audio source on disk.
// Phase 4 is skipped entirely; the sidecar is never called.
func TestIndexClip_AudioChannelLocalPathEmpty(t *testing.T) {
	svc, db, cleanup := newPhase4TestService(t)
	defer cleanup()

	const clipID = "clip-audio-no-path"
	_, err := db.Exec(`
INSERT INTO media_assets (id, name, source, media_type, local_path, search_text, metadata_json)
VALUES (?, 'Test Audio NoPath', 'youtube', 'video', '', 'placeholder search text', '{}')
`, clipID)
	require.NoError(t, err)
	preSeedSourceVersion(t, svc, clipID)

	var audioCalled int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/index" {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]any{
				"clip_id":   clipID,
				"embedding": []float64{0.1, 0.2, 0.3},
			})
			return
		}
		if strings.HasPrefix(r.URL.Path, "/embed_audio") {
			atomic.AddInt32(&audioCalled, 1)
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()
	svc.cfg.ServerURL = server.URL

	err = svc.IndexClip(context.Background(), clipID)
	require.NoError(t, err)

	assert.Equal(t, int32(0), atomic.LoadInt32(&audioCalled),
		"Phase 4 must skip /embed_audio_from_file when local_path is empty")
	persisted := readAudioEmbedding(t, db, clipID)
	assert.Nil(t, persisted)
}
