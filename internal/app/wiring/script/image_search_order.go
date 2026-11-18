package script

import (
	"hash/fnv"
	"math/rand"
	"sort"
	"strings"

	imagesrouting "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images"
	adapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
)

// imageSearchShuffleSeed derives a stable permutation from the retrieval
// subject. The same entity/language/class always sees the same durable rows in
// the same order; different subjects still get different image variants.
func imageSearchShuffleSeed(req adapters.InternetImageSearchRequest) int64 {
	h := fnv.New64a()
	for _, part := range []string{req.Entity, req.Query, req.Language, req.QueryClass} {
		_, _ = h.Write([]byte(strings.ToLower(strings.TrimSpace(part))))
		_, _ = h.Write([]byte{0})
	}
	return int64(h.Sum64())
}

// orderExistingImageResults makes a warm catalog replay reproducible. Catalog
// rows can arrive in any order; sorting by content identity before the seeded
// shuffle means the same candidate set always produces the same order.
func orderExistingImageResults(rows []imagesrouting.ImageSearchResult, seed int64) []imagesrouting.ImageSearchResult {
	out := append([]imagesrouting.ImageSearchResult(nil), rows...)
	sort.SliceStable(out, func(i, j int) bool {
		return imageSearchResultOrderKey(out[i]) < imageSearchResultOrderKey(out[j])
	})
	rng := rand.New(rand.NewSource(seed))
	rng.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	return out
}

func imageSearchResultOrderKey(row imagesrouting.ImageSearchResult) string {
	for _, value := range []string{row.ContentSHA256, row.AssetID, row.PreviewURL, row.Name} {
		if key := strings.ToLower(strings.TrimSpace(value)); key != "" {
			return key
		}
	}
	return ""
}
