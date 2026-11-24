package contract

import audiocontract "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contracts/audio"

// AudioPrefetchAssetReport and AudioPrefetchResult are owned by
// contracts/audio.Output; the aliases keep the root facade stable.
type AudioPrefetchAssetReport = audiocontract.AudioPrefetchAssetReport

type AudioPrefetchResult = audiocontract.AudioPrefetchResult
