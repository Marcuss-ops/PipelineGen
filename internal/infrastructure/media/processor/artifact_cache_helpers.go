package processor

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"go.uber.org/zap"

	capcache "github.com/Marcuss-ops/PipelineGen/internal/capabilities/artifactcache"
	fileutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
)

func capcacheKey(sourceSHA, operation string, params any, version string) capcache.Key {
	body, err := json.Marshal(params)
	if err != nil {
		body = []byte(`{}`)
	}
	return capcache.Key{SourceSHA256: sourceSHA, Operation: operation, ParametersJSON: string(body), ProcessorVersion: version}
}

func hashFileSHA256(path string) string {
	if path == "" {
		return ""
	}
	digest, err := fileutil.HashFile(path, sha256.New())
	if err != nil {
		return ""
	}
	return digest
}

func (p *Processor) materializeCachedFile(ctx context.Context, key capcache.Key, destination string) (bool, string) {
	if p == nil || p.artifactCache == nil || key.SourceSHA256 == "" || destination == "" {
		return false, ""
	}
	var entry *capcache.Entry
	leaseID := ""
	var hit bool
	if claimer, supportsClaims := p.artifactCache.(capcache.ClaimStore); supportsClaims {
		claim, claimErr := claimer.Claim(ctx, key, 15*time.Minute, expectedWorkMS(key.Operation))
		if claimErr == nil {
			if claim.Acquired {
				return false, claim.LeaseID // this worker owns the build lease
			}
			entry, hit = claim.Entry, claim.Entry != nil
		} else {
			p.log.Warn("artifact cache claim failed; falling back to lookup", zap.Error(claimErr))
		}
	}
	if entry == nil && !hit {
		var lookupErr error
		entry, hit, lookupErr = p.artifactCache.Lookup(ctx, key, expectedWorkMS(key.Operation))
		if lookupErr != nil {
			p.log.Warn("artifact cache lookup failed; recomputing", zap.Error(lookupErr))
			return false, ""
		}
	}
	if !hit || entry == nil {
		return false, ""
	}
	reader, err := p.artifactCache.Open(ctx, entry)
	if err != nil {
		p.log.Warn("artifact cache open failed; invalidating entry", zap.Error(err))
		_ = p.artifactCache.Invalidate(ctx, key)
		return false, ""
	}
	defer reader.Close()
	if err := writeAtomicFromReader(destination, reader); err != nil {
		p.log.Warn("artifact cache materialization failed; invalidating entry", zap.Error(err))
		_ = p.artifactCache.Invalidate(ctx, key)
		return false, ""
	}
	return true, leaseID
}

func (p *Processor) storeCachedFile(ctx context.Context, key capcache.Key, leaseID, path, mime string) {

	if p == nil || p.artifactCache == nil || key.SourceSHA256 == "" || path == "" {
		return
	}
	file, err := os.Open(path)
	if err != nil {
		p.releaseCachedClaim(ctx, key, leaseID, err.Error())
		return
	}
	defer file.Close()
	var storeErr error
	if leaseID != "" {
		if leaseStore, ok := p.artifactCache.(capcache.LeaseStore); ok {
			_, storeErr = leaseStore.StoreWithLease(ctx, key, leaseID, file, mime, expectedWorkMS(key.Operation))
		} else {
			_, storeErr = p.artifactCache.Store(ctx, key, file, mime, expectedWorkMS(key.Operation))
		}
	} else {
		_, storeErr = p.artifactCache.Store(ctx, key, file, mime, expectedWorkMS(key.Operation))
	}
	if storeErr != nil {
		p.releaseCachedClaim(ctx, key, leaseID, storeErr.Error())
		p.log.Warn("artifact cache store failed; generated artifact remains valid", zap.Error(storeErr))
	}
}

func (p *Processor) releaseCachedClaim(ctx context.Context, key capcache.Key, leaseID, reason string) {
	if leaseID == "" || p == nil || p.artifactCache == nil {
		return
	}
	if leaseStore, ok := p.artifactCache.(capcache.LeaseStore); ok {
		if err := leaseStore.ReleaseClaim(ctx, key, leaseID, reason); err != nil {
			p.log.Warn("artifact cache lease release failed", zap.Error(err))
		}
	} else {
		_ = p.artifactCache.Invalidate(ctx, key)
	}
}

func expectedWorkMS(operation string) int64 {
	switch operation {
	case "normalize":
		return 1000
	case "proxy":
		return 500
	case "thumbnail":
		return 100
	case "storyboard":
		return 750
	default:
		return 0
	}
}

func writeAtomicFromReader(destination string, reader io.Reader) error {
	if reader == nil {
		return fmt.Errorf("nil cached artifact reader")
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(destination), ".artifact-cache-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = tmp.Close(); _ = os.Remove(tmpPath) }
	defer cleanup()
	if _, err := io.Copy(tmp, reader); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, destination); err != nil {
		return err
	}
	return nil
}
