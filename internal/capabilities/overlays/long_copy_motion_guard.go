package overlays

// longCopyCrashedMotionIDs holds motion ids proven to crash the Chronon
// render daemon on LONG copy. phrase_apple_clean_12_slide_diagonal_pop is
// certified only on short catalog input (motion-certification passed 2026-10-02);
// on a 20-word phrase the daemon rejects its position keyframes
// ("animation property 'position' requires 3-float keyframe values",
// observed live 2026-10-19, deterministic across 3 attempts) and fails the
// whole run. The id stays certified (short catalog input) and the short
// lane already excludes it via its registry blocked_lanes entry, so no
// short path can deal it either; automatic long and heavy rotation must
// never deal it. The heavy lane is long-only in practice
// (short items take the short branch first), so its pool builder filters
// the id even out of explicit caller pools: naming it for heavy copy is a
// guaranteed daemon failure, never a working choice. The long-copy
// verbatim paths (long-selector fallback, planner singleton, run-wide
// uniqueness-guard fallback) filter it the same way: dealing it there is
// also a guaranteed whole-run render failure. Every other explicit
// assignment stays verbatim.
var longCopyCrashedMotionIDs = map[string]struct{}{
	"phrase_apple_clean_12_slide_diagonal_pop": {},
}

// isLongCopyCrashingMotion reports whether the id is proven to crash the
// render daemon on long copy. The planner singleton fast-path uses it to
// decide verbatim-vs-fallback without allocating a filtered pool copy.
func isLongCopyCrashingMotion(id string) bool {
	_, crashed := longCopyCrashedMotionIDs[id]
	return crashed
}

// excludeLongCopyCrashingMotions drops daemon-crashing ids from an automatic
// rotation pool, preserving order. Pools without any guarded id are returned
// untouched. Explicit caller pools pass through here only on the long-copy
// verbatim paths, where keeping the id would fail the whole run at render.
func excludeLongCopyCrashingMotions(pool []string) []string {
	present := false
	for _, id := range pool {
		if _, blocked := longCopyCrashedMotionIDs[id]; blocked {
			present = true
			break
		}
	}
	if !present {
		return pool
	}
	out := make([]string, 0, len(pool))
	for _, id := range pool {
		if _, blocked := longCopyCrashedMotionIDs[id]; !blocked {
			out = append(out, id)
		}
	}
	return out
}
