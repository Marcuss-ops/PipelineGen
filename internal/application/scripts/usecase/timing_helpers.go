package usecase

import "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"

func microseconds(milliseconds int64) (int64, error) {
	return audio.MicrosecondsFromMilliseconds(milliseconds)
}
