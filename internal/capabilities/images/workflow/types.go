// Package images contains external-integration configuration types used by
// the image subsystem.
package workflow

// GoogleAccountingConfig is the optional Google Accounting server
// integration used for asset attribution and Drive-aware placement.
type GoogleAccountingConfig struct {
	// ServerURL is the base URL of the Google Accounting service.
	ServerURL string
	// DownloadDir is the local staging directory for downloaded assets.
	DownloadDir string
	// VidsProjectID is the optional Google Vids project attribution key.
	VidsProjectID string
}
