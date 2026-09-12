package client

import "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"

func resultIsEmpty(result *detail.EntityExtractionResult) bool {
	if result == nil {
		return true
	}
	return len(result.FrasiImportanti) == 0 &&
		len(result.EntitaSenzaTesto) == 0 &&
		len(result.NomiSpeciali) == 0 &&
		len(result.ParoleImportanti) == 0 &&
		len(result.ArtlistPhrases) == 0 &&
		len(result.NounChunks) == 0
}

func capEntityExtractionResult(result *detail.EntityExtractionResult, limit int) *detail.EntityExtractionResult {
	if result == nil {
		return nil
	}
	if limit <= 0 {
		limit = 2
	}
	if len(result.FrasiImportanti) > limit {
		result.FrasiImportanti = result.FrasiImportanti[:limit]
	}
	if len(result.NomiSpeciali) > limit {
		result.NomiSpeciali = result.NomiSpeciali[:limit]
	}
	if len(result.ParoleImportanti) > limit {
		result.ParoleImportanti = result.ParoleImportanti[:limit]
	}
	if len(result.NounChunks) > limit {
		result.NounChunks = result.NounChunks[:limit]
	}
	// Artlist phrases have their own stricter cap (max 5) regardless of the general limit.
	if len(result.ArtlistPhrases) > 5 {
		result.ArtlistPhrases = result.ArtlistPhrases[:5]
	}
	if len(result.EntitaSenzaTesto) > limit {
		capped := make(map[string]string, limit)
		i := 0
		for k, v := range result.EntitaSenzaTesto {
			capped[k] = v
			i++
			if i >= limit {
				break
			}
		}
		result.EntitaSenzaTesto = capped
	}
	return result
}
