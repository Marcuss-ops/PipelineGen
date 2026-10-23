# Audio Definition-of-Done gates. These are headless and deterministic; live
# provider/Velox credentials are deliberately not required by the unit gate.

verify-audio-chunked:
	go test ./internal/capabilities/scripts ./internal/capabilities/scripts/adapters ./internal/capabilities/scripts/usecase
	@echo "✅ CHUNKED_VOICEOVER gate passed"

# The Rust pipelinegen-muscles unit suite is owned by BrainRust CI; these
# gates certify the Go consumer and the real-binary e2e paths.
verify-audio-combined:
	go test ./internal/capabilities/audio ./internal/capabilities/scripts ./internal/platform/media/rustexec
	@echo "✅ COMBINED_TIMELINE gate passed"

verify-audio-copy:
	go test ./internal/platform/media/rustexec -run 'Test(MuxFinalAudioCopy|RequestValidate.*MuxAudioCopy)'
	@echo "✅ FINAL_AUDIO_COPY gate passed"

verify-audio-benchmark:
	go test ./internal/capabilities/audio -run '^$$' -bench 'BenchmarkCompileCanonicalTimeline' -benchtime=1x
	@echo "✅ audio duration benchmark gate passed"

verify-audio-release: verify-audio-chunked verify-audio-combined verify-audio-copy verify-audio-benchmark verify-fast
	@echo "✅ audio release gate passed"
