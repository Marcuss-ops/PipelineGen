// Package workerdoctor — probes_dependency.go (PR-SPLIT-WORKERDOCTOR-PROBES,
// 2026-07-06).
//
// Local / environment dependency probes: config validation, TLS
// material presence, and storage-path writability. These probes
// run at LOCAL layer (the worker's own filesystem + config +
// loaded TLS material), unlike the liveness probes (which run
// at NETWORK layer) and the invariant probes (which run at
// PROCESS layer).
//
// godlike/06 SSOT: this file is the canonical SOLE owner of the
// "is the worker's local environment ready to boot?" surface.
// Mapping note: the user spec referred to "DB/Qdrant/Drive deps"
// — those services are NOT probed directly at this layer because
// the doctor is a stand-alone pre-boot tool. The canonical DB /
// Qdrant / Drive checks live in the master's /ready handler
// which this doctor polls via WireReady() (probes_liveness.go).
// What we probe HERE is the WORKER's local preconditions to
// participate in the cluster: config validity, cert material
// presence, and filesystem writability.
//
// The shared helper ensureWritable is co-located here rather than
// in a shared helpers file per AGENTS.md Pattern 5 one-canonical-
// owner-per-fact — it has no callers outside probeFilesystem.
package workerdoctor

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	tlsload "github.com/Marcuss-ops/PipelineGen/pkg/tlsload"
)

// probeConfig runs cfg.Validate() and reports the aggregated
// outcome. The error is surfaced verbatim under Error so the
// operator sees the same line they would see at boot. No
// duplication of validation logic — Validate() is reused edge-to-edge.
func probeConfig(cfg DoctorConfig) ProbeReceipt {
	if cfg == nil {
		return ProbeReceipt{
			OK:         false,
			Applicable: true,
			Error:      "config is nil",
		}
	}
	if err := cfg.Validate(); err != nil {
		return ProbeReceipt{
			OK:         false,
			Applicable: true,
			Error:      err.Error(),
			Extras: map[string]any{
				"server_port":        cfg.ServerPort(),
				"mtls_enabled":       cfg.MTLSEnabled(),
				"server_tls_enabled": cfg.ServerTLSEnabled(),
			},
		}
	}
	return ProbeReceipt{
		OK:         true,
		Applicable: true,
		Extras: map[string]any{
			"server_port": cfg.ServerPort(),
			"master_url":  cfg.ResolvedMasterURL(),
		},
	}
}

// probeCert runs when either Server.TLS.Enabled or Workers.MTLS.Enabled
// is set. We reuse pkg/tlsload; it already handles key perms, expiry
// windows, and CA↔cert chain alignment. When neither side requires
// mTLS, the check opts out cleanly.
//
// Today DoctorConfig returns false for both sides (canonical
// Config lacks the sub-structs; see config_adapter.go). When
// the adapter becomes a pass-through, this probe automatically
// activates.
func probeCert(cfg DoctorConfig) ProbeReceipt {
	if cfg == nil {
		return ProbeReceipt{OK: false, Applicable: true, Error: "config is nil"}
	}
	if !cfg.ServerTLSEnabled() && !cfg.MTLSEnabled() {
		return ProbeReceipt{
			OK:         true,
			Applicable: false,
			Note:       "mTLS not enabled (server.tls.enabled=false and workers.mtls.enabled=false)",
		}
	}
	// Examines only the side that's enabled. We do not run a real
	// handshake here (that's the worker's job); we only confirm the
	// material on disk will be accepted by the TLS loader.
	extras := map[string]any{}
	var lastErr error
	if cfg.MTLSEnabled() {
		window := time.Duration(cfg.MinMTLSCertTTL()) * 24 * time.Hour
		_, ident, err := tlsload.LoadClientIdentity(
			cfg.MTLSCertFile(),
			cfg.MTLSKeyFile(),
			cfg.MTLSCAFile(),
			cfg.MTLSServerName(),
			window,
		)
		if err != nil {
			lastErr = err
		} else if ident != nil {
			extras["client_cert_fingerprint_sha256"] = ident.FingerprintSHA256
			extras["client_cert_subject_dn"] = ident.SubjectDN
			extras["client_cert_not_after"] = ident.NotAfter
		}
	}
	if lastErr == nil && cfg.ServerTLSEnabled() {
		window := time.Duration(cfg.MinServerTLSCertTTL()) * 24 * time.Hour
		_, ident, err := tlsload.LoadServerIdentity(
			cfg.ServerTLSCertFile(),
			cfg.ServerTLSKeyFile(),
			cfg.ServerTLSClientCAFile(),
			window,
		)
		if err != nil {
			lastErr = err
		} else if ident != nil {
			extras["server_cert_fingerprint_sha256"] = ident.FingerprintSHA256
			extras["server_cert_subject_dn"] = ident.SubjectDN
			extras["server_cert_not_after"] = ident.NotAfter
		}
	}
	if lastErr != nil {
		return ProbeReceipt{
			OK:         false,
			Applicable: true,
			Error:      lastErr.Error(),
			Extras:     extras,
		}
	}
	return ProbeReceipt{
		OK:         true,
		Applicable: true,
		Extras:     extras,
	}
}

// probeFilesystem walks the canonical storage paths derived from
// cfg.Storage and verifies each one is creatable and writable (or
// already exists). Uses StorageConfig.<X>FullPath() helpers — same
// source of truth as the bootstrap composition root.
//
// Failure modes:
//   - missing parent AND cannot create: NOT_READY
//   - exists but not writable: NOT_READY
//   - missing parent, mkdir succeeds: PASS
//
// We do NOT call Stat-then-touch on stale paths; mkDirAll handles
// the race window.
func probeFilesystem(cfg DoctorConfig) ProbeReceipt {
	if cfg == nil {
		return ProbeReceipt{OK: false, Applicable: true, Error: "config is nil"}
	}
	paths := []struct {
		Name string
		Path string
	}{
		{"data_dir", cfg.DataDir()},
		{"primary_db_path", cfg.PrimaryDBFullPath()},
		{"observability_db_path", cfg.ObservabilityDBFullPath()},
		{"workspace_dir", cfg.WorkspaceFullPath()},
		{"cache_dir", cfg.CacheFullPath()},
		{"export_dir", cfg.ExportFullPath()},
	}
	missing := make([]string, 0, len(paths))
	for _, p := range paths {
		if p.Path == "" {
			missing = append(missing, p.Name+"=empty")
			continue
		}
		if err := ensureWritable(p.Path); err != nil {
			missing = append(missing, p.Name+":"+err.Error())
		}
	}
	if len(missing) > 0 {
		return ProbeReceipt{
			OK:         false,
			Applicable: true,
			Error:      "one or more storage paths are not writable",
			Extras: map[string]any{
				"missing_or_unwritable": missing,
			},
		}
	}
	return ProbeReceipt{
		OK:         true,
		Applicable: true,
		Extras: map[string]any{
			"data_dir":        cfg.DataDir(),
			"primary_db_path": cfg.PrimaryDBFullPath(),
			"workspace_dir":   cfg.WorkspaceFullPath(),
			"cache_dir":       cfg.CacheFullPath(),
		},
	}
}

// ensureWritable is os.MkdirAll + a write probe. It can be called on
// either a directory or a file path; for files we MkdirAll the parent
// then touch the file. Cheap and avoids a "real" probe that would
// create artifacts we don't want to own.
func ensureWritable(p string) error {
	if strings.HasSuffix(p, ".sqlite") || strings.HasSuffix(p, ".db") {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		f, err := os.OpenFile(p, os.O_RDWR|os.O_CREATE, 0o600)
		if err != nil {
			return err
		}
		return f.Close()
	}
	if err := os.MkdirAll(p, 0o755); err != nil {
		return err
	}
	// Probe write: try opening a temp file in the directory.
	tmp := filepath.Join(p, ".doctor-write-probe")
	f, err := os.OpenFile(tmp, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_ = f.Close()
	_ = os.Remove(tmp)
	return nil
}
