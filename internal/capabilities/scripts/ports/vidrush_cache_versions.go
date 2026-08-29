package ports

// CacheVersion is bumped when the representation or semantics of a stage
// changes. Including it in every namespace prevents stale cross-version hits.
type CacheVersion string

const (
	SemanticProfileCacheVersion CacheVersion = "semantic-profile-v1"
	DiscoveryCacheVersion       CacheVersion = "youtube-discovery-v1"
	MetadataCacheVersion        CacheVersion = "youtube-metadata-v1"
	TranscriptCacheVersion      CacheVersion = "youtube-transcript-v1"
	PlannedWindowCacheVersion   CacheVersion = "youtube-window-v1"
	MaterializedAssetVersion    CacheVersion = "materialized-asset-v1"
)

func VersionedCacheNamespace(stage string, version CacheVersion) string {
	return stage + "@" + string(version)
}
