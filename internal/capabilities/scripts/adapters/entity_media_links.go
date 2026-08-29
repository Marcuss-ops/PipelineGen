package adapters

import (
	"strings"

	entitycatalog "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// BuildEntityMediaLinks enriches a segment with canonical joins while leaving
// the original NLP entities untouched. Only PERSON entities are canonicalized
// through the existing catalog identity contract.
func BuildEntityMediaLinks(segment scriptpkg.VidRushSegmentResult, assetIDsByCanonical map[string][]string) []scriptpkg.EntityMediaLink {
	links := make([]scriptpkg.EntityMediaLink, 0)
	for _, entity := range segment.Insights.Entities {
		if !strings.EqualFold(strings.TrimSpace(entity.Type), "PERSON") {
			continue
		}
		identity, err := entitycatalog.CanonicalizePersonName(entity.Value)
		if err != nil || identity.CanonicalEntityID == "" {
			continue
		}
		links = append(links, scriptpkg.EntityMediaLink{SurfaceValue: entity.Value, EntityType: entity.Type, CanonicalEntityID: identity.CanonicalEntityID, AssetIDs: append([]string(nil), assetIDsByCanonical[identity.CanonicalEntityID]...)})
	}
	return links
}

func AttachEntityMediaLinks(segment scriptpkg.VidRushSegmentResult, assetIDsByCanonical map[string][]string) scriptpkg.VidRushSegmentResult {
	out := cloneVidRushSegmentResult(segment)
	out.Insights.EntityMediaLinks = BuildEntityMediaLinks(segment, assetIDsByCanonical)
	return out
}
