package overlayjoin

// maxFilterThreads is the ceiling of the filter-graph thread budget.
//
// The join pins one decoder thread per input (-threads 1) because ffmpeg's
// default frame-threaded decoders reserve a reference pool each: with 74
// certified clips that reached 11.7 GiB and was killed at 15.6 GiB in
// production. The same change pinned the filter graph to a single thread, and
// that pin — not the decoder bound — is what serialized the overlay chain.
//
// Measured 2026-11-21 on the r8 biography timeline (13 clips, 991 s,
// 1920x1080@24, 30 s sample, median of runs, host under production load):
// 21.5-25.2 ms/frame at one filter thread versus 17.2-18.3 at four. Six and
// eight threads did not beat four repeatably while other jobs shared the host,
// and every thread count produced byte-identical output (same md5 across
// repeats and across thread counts). Peak RSS on the same 13-clip sample was
// flat (1.70 GiB at one thread, 1.66 GiB at six), so the budget can follow the
// clip count without moving the memory bound the decoder threads set.
const maxFilterThreads = 4

// filterThreads is the filter-graph thread budget for a join with n clips.
// It never returns less than one: a single-threaded graph is the historical
// behaviour and the safe floor.
func filterThreads(clips int) int {
	if clips > maxFilterThreads {
		return maxFilterThreads
	}
	if clips < 1 {
		return 1
	}
	return clips
}
