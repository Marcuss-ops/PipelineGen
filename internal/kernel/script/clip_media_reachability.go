package script

// clip_media_reachability.go — the one owner of "can this bound clip actually
// be resolved to bytes?", shared by POST /api/script/preflight and the
// cmd/velox Stage 0 gate.
//
// Why (2026-11-26, OVL-06): the preflight answered a missing clip with the
// single line "asset non trovato nel catalogo", and the scene-text resolver
// masked its own failure behind "clip X has no Drive link". Neither told an
// operator whether the media was missing locally (rehydratable from Drive) or
// whether the clip had no Drive copy at all (not rehydratable), and neither
// named the path the resolver expects. The verdict below is pure: it reads the
// catalog facts and returns fatal problems plus non-fatal warnings, so the
// server route and the CLI cannot disagree about which of the two cases they
// are looking at.

import (
	"fmt"
	"strings"
)

// ClipMediaReachability is the physical-location slice of one catalog asset.
//
// HasLocalFile / HasDriveFile come from the location projection (the catalog's
// own answer about the bytes on disk / in Drive); LocalPath and DriveLink come
// from the asset metadata and are what the resolver actually uses.
type ClipMediaReachability struct {
	ClipID       string
	LocalPath    string
	DriveLink    string
	HasLocalFile bool
	HasDriveFile bool
}

// ClipMediaProblems returns the fatal reachability problems for one clip.
//
// A clip with no resolvable local media AND no Drive copy can never render:
// the scene-text resolver needs the local binary (or a Drive-only reference
// when the segment allows it), so the run would fail minutes later. That case
// is fatal and names the path the resolver expects.
//
// A clip whose local media is missing but which still has a Drive copy is NOT
// fatal: the Drive-only path can still resolve it. It is reported as a warning
// so the operator sees the degradation before the render, not as a false green.
func ClipMediaProblems(f ClipMediaReachability) (problems, warnings []string) {
	localPath := strings.TrimSpace(f.LocalPath)
	localUsable := f.HasLocalFile && localPath != ""
	if localUsable {
		return nil, nil
	}

	var reason string
	switch {
	case localPath == "" && f.HasLocalFile:
		reason = "local_path non registrato nel catalogo (la location locale esiste)"
	case localPath == "":
		reason = "local_path non registrato nel catalogo"
	default:
		reason = "atteso " + localPath + " (file assente o illeggibile)"
	}

	driveLink := strings.TrimSpace(f.DriveLink)
	switch {
	case driveLink != "":
		warnings = append(warnings, fmt.Sprintf(
			"%s: media locale non risolvibile — %s; copia Drive disponibile: %s (il render procede solo per i segmenti drive-only)",
			f.ClipID, reason, driveLink))
	case f.HasDriveFile:
		warnings = append(warnings, fmt.Sprintf(
			"%s: media locale non risolvibile — %s; copia Drive registrata ma senza drive_link (drive_file_id presente): reidratare il link prima del render",
			f.ClipID, reason))
	default:
		problems = append(problems, fmt.Sprintf(
			"%s: media locale non risolvibile — %s; nessun drive_link né drive_file_id nel catalogo: il clip non è reidratabile",
			f.ClipID, reason))
	}
	return problems, warnings
}
