package youtube

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	capcache "github.com/Marcuss-ops/PipelineGen/internal/capabilities/artifactcache"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"go.uber.org/zap"
)

// CachedWhisperTranscriber decorates the canonical Whisper adapter. The
// transcript result is itself a derived artifact serialized into CAS; the
// local source path is never used as cache identity.
type CachedWhisperTranscriber struct {
	inner   WhisperTranscriber
	cache   capcache.Cache
	version string
	log     *zap.Logger
}

func NewCachedWhisperTranscriber(inner WhisperTranscriber, cache capcache.Cache, processorVersion string, log *zap.Logger) (*CachedWhisperTranscriber, error) {
	if inner == nil {
		return nil, fmt.Errorf("cached whisper: inner transcriber is required")
	}
	if cache == nil {
		return nil, fmt.Errorf("cached whisper: cache is required")
	}
	if processorVersion == "" {
		processorVersion = "whisper/unknown"
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &CachedWhisperTranscriber{inner: inner, cache: cache, version: processorVersion, log: log}, nil
}

var _ WhisperTranscriber = (*CachedWhisperTranscriber)(nil)

func (w *CachedWhisperTranscriber) TranscribeAudio(ctx context.Context, localPath string) (string, error) {
	result, err := w.TranscribeAudioWithDetection(ctx, localPath)
	if err != nil {
		return "", err
	}
	return result.Text, nil
}

func (w *CachedWhisperTranscriber) TranscribeAudioWithDetection(ctx context.Context, localPath string) (asset.TranscriptResult, error) {
	if w == nil || w.inner == nil {
		return asset.TranscriptResult{}, fmt.Errorf("cached whisper: not wired")
	}
	key, ok := whisperCacheKey(localPath, w.version)
	claimed := false
	leaseID := ""
	if ok {
		if claimer, supportsClaims := w.cache.(capcache.ClaimStore); supportsClaims {
			claim, claimErr := claimer.Claim(ctx, key, 15*time.Minute, 5000)
			if claimErr == nil {
				claimed = claim.Acquired
				leaseID = claim.LeaseID
				if claim.Entry != nil {
					if result, readErr := w.readCached(ctx, claim.Entry); readErr == nil {
						w.log.Debug("whisper artifact cache hit", zap.String("source_sha256", key.SourceSHA256))
						return result, nil
					}
					w.log.Warn("whisper artifact cache entry could not be read; recomputing", zap.String("cache_key", claim.Entry.CacheKey))
					_ = w.cache.Invalidate(ctx, key)
				}
			} else {
				w.log.Warn("whisper artifact cache claim failed; falling back to lookup", zap.Error(claimErr))
			}
		}
		if !claimed {
			if entry, hit, err := w.cache.Lookup(ctx, key, 5000); err == nil && hit {
				if result, readErr := w.readCached(ctx, entry); readErr == nil {
					w.log.Debug("whisper artifact cache hit", zap.String("source_sha256", key.SourceSHA256))
					return result, nil
				}
				w.log.Warn("whisper artifact cache entry could not be read; recomputing", zap.String("cache_key", entry.CacheKey))
				_ = w.cache.Invalidate(ctx, key)
			} else if err != nil {
				w.log.Warn("whisper artifact cache lookup failed; recomputing", zap.Error(err))
			}
		}
	}

	result, err := w.inner.TranscribeAudioWithDetection(ctx, localPath)
	if err != nil {
		// Release an abandoned build lease by invalidating the BUILDING
		// row. Without this, a transient Whisper failure would make all
		// concurrent callers wait for the full lease duration.
		if claimed {
			w.releaseClaim(ctx, key, leaseID, err.Error())
		}
		return asset.TranscriptResult{}, err
	}
	if ok {
		body, marshalErr := json.Marshal(result)
		if marshalErr == nil {
			var storeErr error
			if leaseID != "" {
				if leaseStore, supportsLease := w.cache.(capcache.LeaseStore); supportsLease {
					_, storeErr = leaseStore.StoreWithLease(ctx, key, leaseID, bytesReader(body), "application/json", 5000)
				} else {
					_, storeErr = w.cache.Store(ctx, key, bytesReader(body), "application/json", 5000)
				}
			} else {
				_, storeErr = w.cache.Store(ctx, key, bytesReader(body), "application/json", 5000)
			}
			if storeErr != nil {
				w.releaseClaim(ctx, key, leaseID, storeErr.Error())
				w.log.Warn("whisper artifact cache store failed; returning computed result", zap.Error(storeErr))
			}
		} else {
			w.log.Warn("whisper artifact cache serialization failed; returning computed result", zap.Error(marshalErr))
		}
	}
	return result, nil
}

func (w *CachedWhisperTranscriber) releaseClaim(ctx context.Context, key capcache.Key, leaseID, reason string) {
	if leaseID == "" {
		return
	}
	if leaseStore, ok := w.cache.(capcache.LeaseStore); ok {
		if err := leaseStore.ReleaseClaim(ctx, key, leaseID, reason); err != nil {
			w.log.Warn("whisper artifact cache lease release failed", zap.Error(err))
		}
	} else {
		_ = w.cache.Invalidate(ctx, key)
	}
}

func (w *CachedWhisperTranscriber) readCached(ctx context.Context, entry *capcache.Entry) (asset.TranscriptResult, error) {
	reader, err := w.cache.Open(ctx, entry)
	if err != nil {
		return asset.TranscriptResult{}, err
	}
	defer reader.Close()
	body, err := io.ReadAll(reader)
	if err != nil {
		return asset.TranscriptResult{}, err
	}
	var result asset.TranscriptResult
	if err := json.Unmarshal(body, &result); err != nil {
		return asset.TranscriptResult{}, err
	}
	return result, nil
}

func whisperCacheKey(localPath, version string) (capcache.Key, bool) {
	file, err := os.Open(localPath)
	if err != nil {
		return capcache.Key{}, false
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return capcache.Key{}, false
	}
	return capcache.Key{
		SourceSHA256: hex.EncodeToString(hash.Sum(nil)),
		Operation:    "whisper", ParametersJSON: `{"format":"transcript_result"}`,
		ProcessorVersion: version,
	}, true
}

func bytesReader(body []byte) io.Reader { return bytes.NewReader(body) }
