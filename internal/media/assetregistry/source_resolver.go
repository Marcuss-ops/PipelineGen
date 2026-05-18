package assetregistry

import (
	"strings"

	"velox/go-master/internal/repository/clips"
)

// SourceDefinition defines a canonical source with its aliases and associated repository.
type SourceDefinition struct {
	Canonical string
	Aliases   []string
	MediaType string
}

// StandardSources is the canonical list of all supported sources.
// Update this list when adding a new source — all resolvers use it.
var StandardSources = []SourceDefinition{
	{
		Canonical: "artlist",
		Aliases:   []string{"artlist"},
		MediaType: "video",
	},
	{
		Canonical: "clips",
		Aliases:   []string{"youtube", "clips"},
		MediaType: "video",
	},
	{
		Canonical: "stock",
		Aliases:   []string{"stock"},
		MediaType: "video",
	},
	{
		Canonical: "voiceover",
		Aliases:   []string{"voiceover"},
		MediaType: "audio",
	},
	{
		Canonical: "images",
		Aliases:   []string{"images"},
		MediaType: "image",
	},
}

// sourceAliasMap is a pre-built lookup for O(1) alias resolution.
var sourceAliasMap map[string]string

func init() {
	sourceAliasMap = make(map[string]string)
	for _, def := range StandardSources {
		for _, alias := range def.Aliases {
			sourceAliasMap[strings.ToLower(alias)] = def.Canonical
		}
	}
}

// CanonicalSource resolves any source alias to its canonical name.
// Returns empty string if the source is unknown.
func CanonicalSource(source string) string {
	return sourceAliasMap[strings.ToLower(source)]
}

// IsValidSource checks if a source string (or alias) is known.
func IsValidSource(source string) bool {
	return CanonicalSource(source) != ""
}

// IsClipsSource returns true if the source maps to the clips repository.
func IsClipsSource(source string) bool {
	canonical := CanonicalSource(source)
	return canonical == "artlist" || canonical == "clips" || canonical == "stock"
}

// SourceResolver resolves source strings to their clips.Repository.
// This replaces all hand-written resolveRepo switch statements.
type SourceResolver struct {
	artlistRepo *clips.Repository
	clipsRepo   *clips.Repository
	stockRepo   *clips.Repository
}

// NewSourceResolver creates a resolver with the three standard clip repositories.
func NewSourceResolver(artlistRepo, clipsRepo, stockRepo *clips.Repository) *SourceResolver {
	return &SourceResolver{
		artlistRepo: artlistRepo,
		clipsRepo:   clipsRepo,
		stockRepo:   stockRepo,
	}
}

// ResolveRepo returns the clips.Repository for the given source.
// Returns nil for voiceover and images (they use different repository types).
func (r *SourceResolver) ResolveRepo(source string) *clips.Repository {
	canonical := CanonicalSource(source)
	switch canonical {
	case "artlist":
		return r.artlistRepo
	case "clips", "youtube":
		return r.clipsRepo
	case "stock":
		return r.stockRepo
	case "all", "unified":
		// Return clipsRepo as the primary access point for unified media_assets
		return r.clipsRepo
	default:
		return nil
	}
}
