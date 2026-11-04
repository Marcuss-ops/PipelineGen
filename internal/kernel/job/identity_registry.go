package job

// TypeIdentity is one canonical job-type identity fact: the Go constant name
// (diagnostics only) and its byte-exact wire value.
//
// The type lives here so the forward-prevention gate (percheck_identity_ssot)
// consumes the SSOT directly instead of keeping its own copy of the
// vocabulary. A scanner that hardcodes the literals it protects is a second
// declaration of the fact — exactly the defect this package removes.
type TypeIdentity struct {
	// Const is the Go identifier inside this package.
	Const string
	// Literal is the byte-exact wire value (jobs.type + C3 routing key).
	Literal string
}

// CanonicalTypeIdentities returns every shared job-type identity owned by
// this package. Domain and capability packages alias these; they MUST NOT
// re-declare the literal (percheck_identity_ssot fails closed on that).
//
// The returned slice is built fresh on each call, so the gate cannot mutate
// the registry.
func CanonicalTypeIdentities() []TypeIdentity {
	return []TypeIdentity{
		{"TypeScriptGenerate", TypeScriptGenerate},
		{"TypeScriptGenerateItem", TypeScriptGenerateItem},
		{"TypeImagesGenerate", TypeImagesGenerate},
		{"TypeImageGenerateGoogle", TypeImageGenerateGoogle},
		{"TypeAssetsResolve", TypeAssetsResolve},
		{"TypeMediaClip", TypeMediaClip},
		{"TypeAssetTextMaterialize", TypeAssetTextMaterialize},
		{"TypeVoiceoverGenerate", TypeVoiceoverGenerate},
		{"TypeVoiceoverBatch", TypeVoiceoverBatch},
		{"TypeVoiceoverGenerateItem", TypeVoiceoverGenerateItem},
		{"TypeVoiceoverPromo", TypeVoiceoverPromo},
		{"TypeYouTubeClipExtract", TypeYouTubeClipExtract},
		{"TypeSubtitleGenerate", TypeSubtitleGenerate},
		{"TypeCatalogSync", TypeCatalogSync},
		{"TypeSystemCleanup", TypeSystemCleanup},
		{"TypeDriveFolderSync", TypeDriveFolderSync},
		{"TypeClipRender", TypeClipRender},
	}
}
