// Package renderinggen — queue_client_metrics.go owns the worker-metrics
// decoding helpers: how a reported phase duration (milliseconds or
// microseconds, rounded or sub-millisecond) is read off the metrics map of a
// queue job. Split from queue_client.go to keep that file under the
// max_lines_per_file_strict=600 cap.
package renderinggen

// metricMillis reads a millisecond metric from the worker's metrics map,
// rounding down to whole milliseconds. Absent keys yield 0 (unreported).
func metricMillis(m map[string]float64, key string) int64 {
	if m == nil {
		return 0
	}
	return int64(m[key])
}

// metricMillisEither reads a worker-reported phase duration preferring the
// millisecond key and falling back to the microsecond key (the RenderingGen
// worker reports materialize/render/hash/upload phases in microseconds and
// the drive phase in milliseconds). Absent keys yield 0 (unreported).
func metricMillisEither(m map[string]float64, msKey, usKey string) int64 {
	return metricMillisEitherPrecise(m, msKey, usKey, "")
}

// metricMillisEitherPrecise rounds sub-millisecond values to 1 ms instead of
// truncating them to zero, so phases like probe_ms=0.001 are not lost. It
// prefers msKey, then usKey (us/1000), then altMSKey if provided.
func metricMillisEitherPrecise(m map[string]float64, msKey, usKey, altMSKey string) int64 {
	if m == nil {
		return 0
	}
	if v, ok := m[msKey]; ok && v > 0 {
		if int64(v) > 0 {
			return int64(v + 0.5)
		}
		if v >= 0.0005 {
			return 1
		}
	}
	if usKey != "" {
		if v, ok := m[usKey]; ok && v > 0 {
			ms := v / 1000
			if ms >= 0.5 {
				return int64(ms + 0.5)
			}
			if v >= 0.5 {
				return 1
			}
		}
	}
	if altMSKey != "" {
		if v, ok := m[altMSKey]; ok && v > 0 {
			if int64(v) > 0 {
				return int64(v + 0.5)
			}
			if v >= 0.0005 {
				return 1
			}
		}
	}
	return 0
}

// metricMillisEitherMSPrecise is the ms-variant: all three keys are in
// milliseconds. It exists because chronon_job_encoder_finalize_ms is already
// in ms — treating it as microsecond via metricMillisEitherPrecise divides by
// 1000 and collapses 36ms to 1ms.
func metricMillisEitherMSPrecise(m map[string]float64, msKey, msAltKey, msAlt2Key string) int64 {
	if m == nil {
		return 0
	}
	for _, k := range []string{msKey, msAltKey, msAlt2Key} {
		if k == "" {
			continue
		}
		if v, ok := m[k]; ok && v > 0 {
			if int64(v) > 0 {
				return int64(v + 0.5)
			}
			if v >= 0.0005 {
				return 1
			}
		}
	}
	return 0
}
