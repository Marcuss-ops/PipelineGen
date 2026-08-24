// Package monitor owns the channel-monitor orchestration surface.
//
// God-object cleanup (July 2026): the former catch-all ports.go file was split
// by capability so each contract has one small home:
//   - ports_downloader.go: MonitorDownloaderPort + VideoInfo
//   - ports_transcript.go: TranscriptProvider
//   - ports_analyzer.go: VideoAnalyzer + AnalyzeOptions + sentinels
//   - ports_enqueuer.go: JobEnqueuer + extraction enqueue payloads
//   - ports_channels.go: CategoryChannelsPort
//   - ports_discoveries.go: YoutubeDiscoveriesPort
//   - types.go: shared DTOs, constants, CompositionDeps
package assets
