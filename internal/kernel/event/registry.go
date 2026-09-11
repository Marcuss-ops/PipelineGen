package event

// Identity is one canonical event-identity fact: the Go constant name (used
// only for diagnostics) and its byte-exact literal.
//
// The type lives here so the forward-prevention gate (percheck_identity_ssot)
// can consume the SSOT directly instead of keeping its own copy of the
// vocabulary. A scanner that hardcodes the literals it protects is a second
// declaration of the fact, which is precisely the defect this package
// removes.
type Identity struct {
	// Const is the Go identifier inside this package.
	Const string
	// Literal is the byte-exact provider-scoped value.
	Literal string
}

// Canonical returns every registered canonical event-identity literal.
//
// The returned slice is built fresh on each call, so a caller (the gate)
// cannot mutate the registry.
func Canonical() []Identity {
	return []Identity{
		// Event names.
		{"AssetIndexRequested", AssetIndexRequested},
		{"AssetIndexDeleteRequested", AssetIndexDeleteRequested},
		{"AssetIndexRestoreRequested", AssetIndexRestoreRequested},
		{"AssetDriveDeleteRequested", AssetDriveDeleteRequested},
		{"BindingIndexRequested", BindingIndexRequested},
		{"ScriptGenerateQueued", ScriptGenerateQueued},
		{"VoiceoverCleanupRequested", VoiceoverCleanupRequested},
		{"AssetPublished", AssetPublished},
		{"AssetRightsChanged", AssetRightsChanged},
		{"AssetRightsExtensionBatchApplied", AssetRightsExtensionBatchApplied},
		{"DeliveryRequested", DeliveryRequested},
		{"AssetMetadataExportRequested", AssetMetadataExportRequested},
		{"ProviderSyncRequested", ProviderSyncRequested},
		{"WorkflowStepCompleted", WorkflowStepCompleted},
		{"WorkflowStepFailed", WorkflowStepFailed},
		{"JobCompleted", JobCompleted},
		{"MetadataEnrichRequested", MetadataEnrichRequested},
		{"ArtifactStagedV1", ArtifactStagedV1},

		// Envelope schema versions.
		{"AssetIndexRequestedV1Schema", AssetIndexRequestedV1Schema},
		{"AssetIndexDeleteRequestedV1Schema", AssetIndexDeleteRequestedV1Schema},
		{"AssetIndexRestoreRequestedV1Schema", AssetIndexRestoreRequestedV1Schema},
		{"AssetDriveDeleteRequestedV1Schema", AssetDriveDeleteRequestedV1Schema},
		{"VoiceoverCleanupRequestedV1Schema", VoiceoverCleanupRequestedV1Schema},
		{"AssetPublishedV1Schema", AssetPublishedV1Schema},
		{"AssetRightsChangedV1Schema", AssetRightsChangedV1Schema},
		{"AssetRightsExtensionBatchV1Schema", AssetRightsExtensionBatchV1Schema},
		{"BindingIndexRequestedV1Schema", BindingIndexRequestedV1Schema},
	}
}
