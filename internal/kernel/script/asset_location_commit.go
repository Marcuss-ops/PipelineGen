package script

// AssetLocationChange is the durable state change produced by Drive
// reconciliation for a media asset. DriveLink is empty when the location
// is unusable; DriveFileID is retained when known for diagnosis or republishing.
type AssetLocationChange struct {
	AssetID     string
	DriveFileID string
	DriveLink   string
}
