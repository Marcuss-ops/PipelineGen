// cliprender_transcript_cache.go — local reuse of streaming Whisper results.
//
// The streaming PCM path bypassed the WAV chain's CachedWhisperTranscriber, so
// every retry over identical source bytes paid a full ASR pass again. The
// helpers here persist the typed result in the shared artifact cache (SQLite
// mapping + local CAS bytes) under a source-bytes + language address.
package adapters

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"strings"

	"go.uber.org/zap"

	capcache "github.com/Marcuss-ops/PipelineGen/internal/capabilities/artifactcache"
	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
)

// SetArtifactCache attaches the shared derived-artifact cache. A nil cache is
// a documented no-op: the transcriber still runs, it just cannot reuse an
// earlier transcript. processorVersion must distinguish the bridge (and thus
// the ASR contract) so a changed bridge never reads stale results.
func (s *ClipRenderStreamingTranscriber) SetArtifactCache(cache capcache.Cache, processorVersion string) {
	if s == nil || cache == nil {
		return
	}
	if strings.TrimSpace(processorVersion) == "" {
		processorVersion = "whisper/stream/unknown"
	}
	s.cache = cache
	s.cacheVersion = processorVersion
}

// streamingWhisperCacheOperation namespaces the streaming ASR result in the
// shared artifact cache. It is deliberately distinct from the WAV chain's
// "whisper" operation: the two paths have different decode contracts.
const streamingWhisperCacheOperation = "whisper_stream"

// streamingWhisperCacheKey builds the content-addressed cache key. A missing or
// malformed source digest is not cacheable: an untrustworthy address must never
// be used to serve a transcript.
func streamingWhisperCacheKey(sourceSHA256, language, version string) (capcache.Key, bool) {
	sourceSHA256 = strings.ToLower(strings.TrimSpace(sourceSHA256))
	if len(sourceSHA256) != 64 {
		return capcache.Key{}, false
	}
	if _, err := hex.DecodeString(sourceSHA256); err != nil {
		return capcache.Key{}, false
	}
	version = strings.TrimSpace(version)
	if version == "" {
		return capcache.Key{}, false
	}
	lang := strings.ToLower(strings.TrimSpace(language))
	if lang == "" {
		lang = "und"
	}
	params, err := json.Marshal(struct {
		Language string `json:"language"`
		Format   string `json:"format"`
	}{Language: lang, Format: "transcript_result"})
	if err != nil {
		return capcache.Key{}, false
	}
	return capcache.Key{
		SourceSHA256:     sourceSHA256,
		Operation:        streamingWhisperCacheOperation,
		ParametersJSON:   string(params),
		ProcessorVersion: version,
	}, true
}

func (s *ClipRenderStreamingTranscriber) lookupCachedTranscript(ctx context.Context, key capcache.Key, source *cliprender.MaterializedAsset) (*cliprender.TranscriptResult, bool) {
	entry, hit, err := s.cache.Lookup(ctx, key, 0)
	if err != nil {
		s.log.Warn("streaming transcribe: artifact cache lookup failed; running ASR", zap.Error(err))
		return nil, false
	}
	if !hit || entry == nil {
		return nil, false
	}
	reader, err := s.cache.Open(ctx, entry)
	if err != nil {
		s.log.Warn("streaming transcribe: cached transcript cannot be opened; running ASR",
			zap.String("cache_key", entry.CacheKey), zap.Error(err))
		_ = s.cache.Invalidate(ctx, key)
		return nil, false
	}
	defer func() { _ = reader.Close() }()
	body, err := io.ReadAll(reader)
	if err != nil {
		s.log.Warn("streaming transcribe: cached transcript cannot be read; running ASR",
			zap.String("cache_key", entry.CacheKey), zap.Error(err))
		_ = s.cache.Invalidate(ctx, key)
		return nil, false
	}
	var result cliprender.TranscriptResult
	if err := json.Unmarshal(body, &result); err != nil {
		s.log.Warn("streaming transcribe: cached transcript is invalid; running ASR",
			zap.String("cache_key", entry.CacheKey), zap.Error(err))
		_ = s.cache.Invalidate(ctx, key)
		return nil, false
	}
	// The cached result was computed from the same bytes but may have been
	// addressed through another asset id: keep the current identity.
	result.AssetID = source.AssetID
	result.SourceAudioSHA256 = source.SHA256
	result.Reused = true
	s.log.Debug("streaming transcribe: artifact cache hit",
		zap.String("source_sha256", key.SourceSHA256),
		zap.String("language", result.Language))
	return &result, true
}

func (s *ClipRenderStreamingTranscriber) storeCachedTranscript(ctx context.Context, key capcache.Key, result *cliprender.TranscriptResult) {
	body, err := json.Marshal(result)
	if err != nil {
		s.log.Warn("streaming transcribe: cache serialization failed; result not cached", zap.Error(err))
		return
	}
	if _, err := s.cache.Store(ctx, key, bytes.NewReader(body), "application/json", 0); err != nil {
		s.log.Warn("streaming transcribe: artifact cache store failed; returning computed result", zap.Error(err))
	}
}
