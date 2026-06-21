// Package drive — types.go (PR2.7)
//
// Cross-package Drive file reference type. PR2.7 places DriveFileRef
// here (in internal/infrastructure/drive) so that the DriveFolderManager
// adapter in folder_manager.go can return a domain-shaped slice
// ([]drive.DriveFileRef) WITHOUT importing the artlist application
// package. Without this, the import graph becomes:
//
//	artlist → ... → assets/assetop → drive → artlist  (cycle, rejected)
//
// The artlist application package uses a Go 1.9+ type alias
// (`type DriveFileRef = drive.DriveFileRef` in ports.go) so that
// callers see `artlist.DriveFileRef` as a name while the underlying
// type lives here. Method sets, struct access, and interface-style
// return values remain interchangeable — only the package qualifier
// differs. DriveFileRef is intentionally minimal: just enough metadata
// for application-level consumers to identify a sibling file by name
// when iterating over listing results. The "Name" field is currently
// unused by the only consumer (semantic_enricher reads only .ID) but
// kept for future callers and to keep the adapter's Drive query
// fields stay self-documenting (`files(id, name)`).
package drive

// DriveFileRef is the canonical cross-package representation of a
// Google Drive file reference. Returned by DriveFolderManagerAdapter
// (and may be returned by any future Drive port). Application layer
// ports alias this type so callers can write `artlist.DriveFileRef`
// while the underlying struct lives here.
type DriveFileRef struct {
	ID   string
	Name string
}

// Doc is the canonical Google Docs reference returned by DocClient
// (internal/infrastructure/drive/doc_client.go). It bundles the IDs
// and human-facing metadata needed by callers (CreateDoc, ListRecentDocs,
// UpdateDoc) — content is populated only for newly created docs.
type Doc struct {
	ID        string
	Title     string
	URL       string
	Content   string
	CreatedAt string
}
