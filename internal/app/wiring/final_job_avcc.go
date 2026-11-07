package wiring

// final_job_avcc.go keeps the final-job fragment's packet-copy identity on the
// kernel media owner. The MP4 avcC reader used to live here; it now lives in
// kernel/media so the stock publication gate and the fragment producer read the
// SAME bytes instead of maintaining two parsers that could drift.

import kernelmedia "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"

// readAVCDecoderConfigurationSHA256 is the packet-copy identity of the
// fragment's SPS/PPS. Empty/error means the bytes cannot be certified.
func readAVCDecoderConfigurationSHA256(path string) (string, error) {
	return kernelmedia.ReadAVCDecoderConfigurationSHA256(path)
}
