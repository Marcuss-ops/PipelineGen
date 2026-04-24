package entities

import (
	"fmt"
	"sync"
)

// CachingExtractor wraps another Extractor with an in-memory cache.
type CachingExtractor struct {
	base    Extractor
	cache   sync.Map // key: string (text+count), value: *SegmentEntityResult
}

// NewCachingExtractor creates a new caching extractor
func NewCachingExtractor(base Extractor) *CachingExtractor {
	return &CachingExtractor{
		base: base,
	}
}

// ExtractFromSegment implements Extractor with caching
func (e *CachingExtractor) ExtractFromSegment(text string, segmentIndex int, entityCount int) (*SegmentEntityResult, error) {
	key := fmt.Sprintf("%s|%d", text, entityCount)
	if val, ok := e.cache.Load(key); ok {
		res := val.(*SegmentEntityResult)
		// Return a copy to avoid mutation issues, but updating segmentIndex
		copyRes := *res
		copyRes.SegmentIndex = segmentIndex
		return &copyRes, nil
	}

	result, err := e.base.ExtractFromSegment(text, segmentIndex, entityCount)
	if err != nil {
		return nil, err
	}

	e.cache.Store(key, result)
	return result, nil
}

// ExtractFromScript implements Extractor with caching
func (e *CachingExtractor) ExtractFromScript(segments []string, entityCount int) (*ScriptEntityAnalysis, error) {
	results := make([]SegmentEntityResult, len(segments))
	totalEntities := 0

	for i, segment := range segments {
		res, err := e.ExtractFromSegment(segment, i, entityCount)
		if err != nil {
			// If one fails, we don't fail the whole script, just return empty for this segment
			results[i] = SegmentEntityResult{
				SegmentIndex: i,
				SegmentText:  segment,
			}
			continue
		}
		results[i] = *res
		totalEntities += len(res.FrasiImportanti) + len(res.EntitaSenzaTesto) + len(res.NomiSpeciali) + len(res.ParoleImportanti)
	}

	return &ScriptEntityAnalysis{
		TotalSegments:         len(segments),
		SegmentEntities:       results,
		TotalEntities:         totalEntities,
		EntityCountPerSegment: entityCount,
	}, nil
}
