package acquisition

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"go.uber.org/zap"

	appacq "github.com/Marcuss-ops/PipelineGen/internal/capabilities/acquisition"
)

// metaFileEnvelope is the on-disk JSON shape for {ID}.meta.json.
// Mirrors PrepareContext field-for-field so an audit reader can
// decode the sidecar without the runtime. New fields are added
// in lockstep with PrepareContext additions.
type metaFileEnvelope struct {
	SchemaVersion string           `json:"schema_version"`
	ID            string           `json:"id"`
	SourceRef     appacq.SourceRef `json:"source_ref"`
	LocalPath     string           `json:"local_path"`
	StorageURI    string           `json:"storage_uri,omitempty"`
	SHA256        string           `json:"sha256"`
	SizeBytes     int64            `json:"size_bytes"`
	MIMEType      string           `json:"mime_type"`
	ExpiresAt     time.Time        `json:"expires_at"`
	CleanupToken  string           `json:"cleanup_token"`
}

func (f *FilesystemStager) writeMeta(metaPath string, ctx appacq.PrepareContext) error {
	envelope := metaFileEnvelope{
		SchemaVersion: "v1",
		ID:            ctx.ID,
		SourceRef:     ctx.SourceRef,
		LocalPath:     ctx.LocalPath,
		StorageURI:    ctx.StorageURI,
		SHA256:        ctx.SHA256,
		SizeBytes:     ctx.SizeBytes,
		MIMEType:      ctx.MIMEType,
		ExpiresAt:     ctx.ExpiresAt,
		CleanupToken:  ctx.CleanupToken,
	}
	raw, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	// Atomic write: tmp + rename. The OS rename is atomic on the
	// same filesystem so a partial write can never be observed.
	tmp := metaPath + ".partial"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, metaPath)
}

func (f *FilesystemStager) readMeta(metaPath string) (*appacq.PrepareContext, bool) {
	raw, err := os.ReadFile(metaPath)
	if err != nil {
		return nil, false
	}
	var envelope metaFileEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		// Corrupt meta — treat as cache miss. Operator can
		// delete the .meta.json manually to recover.
		f.log.Warn("acquisition: meta sidecar unreadable; cache miss",
			zap.String("meta_path", metaPath),
			zap.Error(err))
		return nil, false
	}
	if envelope.LocalPath != "" && envelope.SizeBytes != int64(len(raw)) {
		f.log.Warn("acquisition: meta size mismatch; cache miss",
			zap.String("meta_path", metaPath),
			zap.Int64("expected", envelope.SizeBytes),
			zap.Int("actual", len(raw)),
		)
		_ = envelope
	}
	return &appacq.PrepareContext{
		ID:           envelope.ID,
		SourceRef:    envelope.SourceRef,
		LocalPath:    envelope.LocalPath,
		StorageURI:   envelope.StorageURI,
		SHA256:       envelope.SHA256,
		SizeBytes:    envelope.SizeBytes,
		MIMEType:     envelope.MIMEType,
		ExpiresAt:    envelope.ExpiresAt,
		CleanupToken: envelope.CleanupToken,
	}, true
}

// findByToken scans stagingRoot for a .meta.json whose inner
// CleanupToken matches the supplied token. O(N) — acceptable for
// per-run staging surfaces (a few hundred at most).
func (f *FilesystemStager) findByToken(token string) ([]appacq.PrepareContext, error) {
	entries, err := os.ReadDir(f.stagingRoot)
	if err != nil {
		return nil, fmt.Errorf("read staging dir %q: %w", f.stagingRoot, err)
	}
	var out []appacq.PrepareContext
	var errs []string
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".meta.json") {
			continue
		}
		raw, readErr := os.ReadFile(filepath.Join(f.stagingRoot, name))
		if readErr != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", name, readErr))
			continue
		}
		var envelope metaFileEnvelope
		if err := json.Unmarshal(raw, &envelope); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		if envelope.CleanupToken == token {
			out = append(out, appacq.PrepareContext{
				ID:           envelope.ID,
				SourceRef:    envelope.SourceRef,
				LocalPath:    envelope.LocalPath,
				StorageURI:   envelope.StorageURI,
				SHA256:       envelope.SHA256,
				SizeBytes:    envelope.SizeBytes,
				MIMEType:     envelope.MIMEType,
				ExpiresAt:    envelope.ExpiresAt,
				CleanupToken: envelope.CleanupToken,
			})
		}
	}
	if len(errs) > 0 {
		sort.Strings(errs)
		return nil, fmt.Errorf("filesystem scan errors: %s", strings.Join(errs, "; "))
	}
	return out, nil
}
