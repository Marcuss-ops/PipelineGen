package artlist

import "strings"

// ArtlistAcquisitionMode controls how Artlist assets are acquired.
//
//	manual_import - PipelineGen does NOT download automatically. Users
//	                download assets from Artlist and place them in the
//	                import folder; the pipeline ingests them and records
//	                provenance. This is the default (godlike/07 fail-closed).
//	authorized_api  - Automatic search+download is allowed, typically under
//	                  an Enterprise/API agreement. Subject to the daily
//	                  download limit configured in ExternalConfig.
type ArtlistAcquisitionMode string

const (
	// AcquisitionModeManualImport is the fail-closed default: no automatic
	// downloads; users import files manually.
	AcquisitionModeManualImport ArtlistAcquisitionMode = "manual_import"

	// AcquisitionModeAuthorizedAPI enables automatic search+download when
	// the operator has an Enterprise/API agreement in place.
	AcquisitionModeAuthorizedAPI ArtlistAcquisitionMode = "authorized_api"
)

// Normalize returns the canonical acquisition mode. Empty input resolves to
// AcquisitionModeManualImport.
func (m ArtlistAcquisitionMode) Normalize() ArtlistAcquisitionMode {
	switch strings.ToLower(strings.TrimSpace(string(m))) {
	case string(AcquisitionModeAuthorizedAPI):
		return AcquisitionModeAuthorizedAPI
	case string(AcquisitionModeManualImport), "":
		return AcquisitionModeManualImport
	default:
		return AcquisitionModeManualImport
	}
}

// IsValid reports whether the mode is one of the canonical enum values.
func (m ArtlistAcquisitionMode) IsValid() bool {
	switch m.Normalize() {
	case AcquisitionModeManualImport, AcquisitionModeAuthorizedAPI:
		return true
	}
	return false
}

// AllowsAutomaticDownload reports whether this mode permits automatic
// downloads. Even when true, the daily limit must also be > 0.
func (m ArtlistAcquisitionMode) AllowsAutomaticDownload() bool {
	return m.Normalize() == AcquisitionModeAuthorizedAPI
}
