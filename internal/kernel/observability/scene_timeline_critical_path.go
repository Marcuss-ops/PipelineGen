// kernel/observability/scene_timeline_critical_path.go —
//
// Critical path FROM the per-scene trace (§7.3: "calcolare il critical path
// dalla trace, senza sommare work concorrente"). The computation is a longest
// path over the declared per-scene dependency DAG:
//
//	text → {translation, nlp, tts}   (fan-out from SceneTextReady)
//	translation → tts                (the target-language lane)
//	nlp → asset
//	{translation, tts, asset} → audio (the join)
//
// Weights:
//   - node (entry)  = its measured WORK span (completed − started);
//   - edge p → e    = the observed gap between p's completion and e's start
//     (clamped at 0: pipelining never invents negative latency);
//   - root edge     = a root entry's own ready→start gap when it recorded one.
//
// Because a path traverses ONE branch of every fan-out, concurrent work is
// never summed: two 50 ms sibling branches contribute 50 ms to the path, not
// 100 ms. The result is the chain that actually gates the scene flow.
package observability

// sceneStagePredecessors is the DECLARED per-scene dependency DAG (§7.3).
// Stages absent from the map have no per-scene predecessor (roots: text).
var sceneStagePredecessors = map[SceneStage][]SceneStage{
	SceneStageText: {},
	// tts chains to the text AND to the translation of its OWN language: a
	// scene already in the target language has no translation lane and starts
	// straight from the text anchor.
	SceneStageTranslation: {SceneStageText},
	SceneStageNLP:         {SceneStageText},
	SceneStageTTS:         {SceneStageText, SceneStageTranslation},
	SceneStageAsset:       {SceneStageNLP},
	SceneStageAudio:       {SceneStageTranslation, SceneStageTTS, SceneStageAsset},
}

// sceneStageDepth orders stages topologically so a stage's predecessors are
// always resolved before it during the longest-path sweep.
var sceneStageDepth = map[SceneStage]int{
	SceneStageText:        0,
	SceneStageTranslation: 1,
	SceneStageNLP:         1,
	SceneStageTTS:         1,
	SceneStageAsset:       2,
	SceneStageAudio:       3,
}

// SceneCriticalPath is the computed critical chain, ordered root → leaf.
type SceneCriticalPath struct {
	// TotalMs is the chain's accumulated weight (work + dependency gaps).
	TotalMs int64 `json:"total_ms"`
	// Chain is the ordered list of entries that gate the scene flow.
	Chain []SceneStageEntry `json:"chain"`
}

// sceneGapMs is the observed dependency gap between a predecessor's completion
// and a successor's earliest observed instant (see entryStart), clamped at 0.
func sceneGapMs(pred, entry SceneStageEntry) int64 {
	gap := entryStart(entry).Sub(pred.CompletedAt).Milliseconds()
	return nonNegative(gap)
}

// sameSceneLanguage reports whether two entries may hold the same per-scene
// dependency lane. An empty language is a wildcard: run-level stages (nlp,
// asset, audio) are shared across languages.
func sameSceneLanguage(a, b string) bool {
	return a == "" || b == "" || a == b
}

// CriticalPath computes the longest dependency chain through the trace. It
// returns nil for an empty trace. Entries recorded without a measurable span
// cannot exist (RecordSceneStage enforces the span), so every node carries a
// real weight.
func (t *SceneTimeline) CriticalPath() *SceneCriticalPath {
	if t == nil || len(t.Entries) == 0 {
		return nil
	}
	entries := t.Entries

	// Group candidate predecessors by (scene, stage) once.
	type lane struct {
		scene string
		stage SceneStage
	}
	byLane := make(map[lane][]int, len(entries))
	for i := range entries {
		key := lane{scene: entries[i].SceneID, stage: entries[i].Stage}
		byLane[key] = append(byLane[key], i)
	}

	// best[i] is the weight of the heaviest chain ENDING at entry i; from[i]
	// is the entry the chain arrived from (-1 = root); depth[i] is the chain's
	// node count. Equal-weight arrivals are broken toward the predecessor that
	// completed LAST — the one that actually gated the successor's start — then
	// toward the deeper chain (same wall, more attribution).
	best := make([]int64, len(entries))
	from := make([]int, len(entries))
	depth := make([]int, len(entries))
	roots := make([]bool, len(entries))

	// Sweep in stage-depth order (stable within a depth: entry order).
	order := make([]int, 0, len(entries))
	for depth := 0; depth <= 3; depth++ {
		for i := range entries {
			if sceneStageDepth[entries[i].Stage] == depth {
				order = append(order, i)
			}
		}
	}
	// Entries of undeclared stages sort last and degrade to isolated roots.
	for i := range entries {
		if _, declared := sceneStagePredecessors[entries[i].Stage]; !declared {
			order = append(order, i)
		}
	}

	for _, i := range order {
		entry := entries[i]
		work := entry.WorkMs
		preds := sceneStagePredecessors[entry.Stage]

		arrivedFrom := -1
		var arrivedBest int64
		for _, predStage := range preds {
			for _, p := range byLane[lane{scene: entry.SceneID, stage: predStage}] {
				if !sameSceneLanguage(entries[p].Language, entry.Language) {
					continue
				}
				candidate := best[p] + sceneGapMs(entries[p], entry)
				if arrivedFrom == -1 || candidate > arrivedBest ||
					(candidate == arrivedBest && entries[p].CompletedAt.After(entries[arrivedFrom].CompletedAt)) ||
					(candidate == arrivedBest && entries[p].CompletedAt.Equal(entries[arrivedFrom].CompletedAt) && depth[p] > depth[arrivedFrom]) {
					arrivedFrom, arrivedBest = p, candidate
				}
			}
		}
		if arrivedFrom == -1 {
			// Trace root: no recorded predecessor. Charge the entry's own
			// ready→start gap when it recorded one, else just its work.
			var readyGap int64
			if !entry.ReadyAt.IsZero() && entryStart(entry).After(entry.ReadyAt) {
				readyGap = entryStart(entry).Sub(entry.ReadyAt).Milliseconds()
			}
			roots[i] = true
			best[i] = readyGap + work
			from[i] = -1
			depth[i] = 1
			continue
		}
		from[i] = arrivedFrom
		best[i] = arrivedBest + work
		depth[i] = depth[arrivedFrom] + 1
	}

	// The chain is the heaviest chain ENDING anywhere: its leaf is the entry
	// with the maximum best. Equal weights are broken toward the DEEPER chain,
	// then toward the later completion: when a final stage costs less than the
	// millisecond resolution (or zero, like an audio join anchor), the path must
	// still end at that leaf instead of stopping at an intermediate node.
	leaf := 0
	for i := range entries {
		if best[i] > best[leaf] ||
			(best[i] == best[leaf] && depth[i] > depth[leaf]) ||
			(best[i] == best[leaf] && depth[i] == depth[leaf] && entries[i].CompletedAt.After(entries[leaf].CompletedAt)) {
			leaf = i
		}
	}
	chain := make([]SceneStageEntry, 0, 8)
	for i := leaf; i >= 0; i = from[i] {
		chain = append(chain, entries[i])
		if roots[i] {
			break
		}
	}
	// Reverse to root → leaf order.
	for l, r := 0, len(chain)-1; l < r; l, r = l+1, r-1 {
		chain[l], chain[r] = chain[r], chain[l]
	}
	return &SceneCriticalPath{TotalMs: best[leaf], Chain: chain}
}

// SceneStageWorkMs returns the measured work of one (scene, stage, language)
// lane — the sum is over that lane's entries only (e.g. per-language retries),
// never over concurrent lanes.
func (t *SceneTimeline) SceneStageWorkMs(sceneID string, stage SceneStage, language string) int64 {
	if t == nil {
		return 0
	}
	var total int64
	for _, e := range t.Entries {
		if e.SceneID == sceneID && e.Stage == stage && sameSceneLanguage(e.Language, language) {
			// Entry.WorkMs is the only work authority: an anchor entry without a
			// measured start must contribute 0, never completed−zero-time.
			total += e.WorkMs
		}
	}
	return total
}
