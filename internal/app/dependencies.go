// Package app — shared dependency-graph helpers (PR4d-final + PR4.3/PR4.4 extraction).
//
// PR4d-final (June 2026): this file no longer carries `type services struct`
// or the `initServices`/`composeCoreInfra`/`composeMediaDomain`/`composeIntegration`
// orchestrators. Those legacy structs/functions were duplicates of the
// canonical bundle decomposition in composition.go (Drive/Repo/Search/Process/
// AI/Domain/Jobs/Outbox/Sync/Maint/Utility + NewComposition).
//
// PR4.3/PR4.4 (June 2026): the three service-construction helpers were
// extracted to per-capability files:
//   - initVoiceoverService → compose_voiceover.go
//   - initBooksService      → compose_content.go
//   - initImageService      → compose_images.go
//
// This file now serves as the package doc for the PR4-era cleanup;
// BuildDomainBundle (composition.go) still calls the extracted helpers
// which remain in package app.
package app
