package adapters

import "testing"

func TestVidRushCacheMetricsCountHitMissAndStore(t *testing.T) {
	before := VidRushCacheMetrics()
	cacheStore(vidrushImageCache, "metrics-test-key", "value")
	if _, ok := cacheLoad(vidrushImageCache, "metrics-test-key"); !ok {
		t.Fatal("stored key was not found")
	}
	if _, ok := cacheLoad(vidrushImageCache, "metrics-test-missing"); ok {
		t.Fatal("missing key unexpectedly hit")
	}
	after := VidRushCacheMetrics()
	if after.Stores-before.Stores < 1 || after.Hits-before.Hits < 1 || after.Misses-before.Misses < 1 {
		t.Fatalf("cache metrics delta = %+v, want at least one store, hit and miss", after)
	}
}
