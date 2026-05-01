package association

import (
	"strings"

	"velox/go-master/pkg/sliceutil"
)

// ApplyHints applica i suggerimenti di associazione a un segmento.
// Questo metodo è pensato per essere usato dal chiamante per iniettare i risultati del matching nel proprio modello.
func ApplyHints(best Candidate, setPreferred func(reason, group string, paths []string)) {
	preferredLink := best.Link
	if preferredLink == "" && best.FolderID != "" {
		preferredLink = "https://drive.google.com/drive/folders/" + best.FolderID
	}

	paths := sliceutil.UniqueStrings(sliceutil.TrimStrings([]string{best.Path, preferredLink}))
	setPreferred(best.Reason, best.Source, paths)
}

// NormalizeDriveFolderLink assicura che il link alla cartella Drive sia coerente.
func NormalizeDriveFolderLink(driveLink, folderID string) string {
	driveLink = strings.TrimSpace(driveLink)
	folderID = strings.TrimSpace(folderID)
	if driveLink != "" {
		return driveLink
	}
	if folderID != "" {
		return "https://drive.google.com/drive/folders/" + folderID
	}
	return ""
}
