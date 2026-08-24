package assets

import "strings"

// ArtlistAcquisitionMode controls how Artlist assets are acquired.
//
//	manual_import - Operator-pinned opt-out. PipelineGen does NOT download
//	                automatically. Users download assets from Artlist
//	                themselves and place them in the import folder; the
//	                pipeline ingests them and records provenance. Operators
//	                opt IN to this mode EXPLICITLY via env override
//	                ARTLIST_ACQUISITION_MODE=manual_import; the loader
//	                default is now authorized_api (PR-ARTLIST-AUTHORIZED-BY-DEFAULT
//	                P1, July 2026).
//	authorized_api  - Automatic search+download is allowed, typically under
//	                  an Enterprise/API agreement. Subject to the daily
//	                  download limit configured in ExternalConfig (default
//	                  10 per account per day).
type ArtlistAcquisitionMode string

const (
	// AcquisitionModeManualImport is the operator-pinned opt-out (NOT the
	// loader default; see PR-ARTLIST-AUTHORIZED-BY-DEFAULT P1, July 2026). Use case:
	// Artlist Enterprise agreement prohibiting server-side fetches, or environments
	// that require manual curation. Operators opt IN to this mode EXPLICITLY via
	// env override ARTLIST_ACQUISITION_MODE=manual_import.
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
