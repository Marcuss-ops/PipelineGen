// Package drive — types.go
//
// Cross-package Drive file reference type. The DriveFolderManager
// adapter in folder_manager.go returns `[]drive.DriveFileRef` (the
// canonical type declared here) so the infrastructure package does
// NOT need to import the artlist application layer. W16-PR4 removed
// the prior `type artlist.DriveFileRef = drive.DriveFileRef` alias
// from internal/application/assets/providers/artlist/ports.go: callers
// now reference the canonical struct directly via the drivepkg import
// (compile-time assertions on var _ artlistpkg.DriveFolderManager =
// (*drivepkg.DriveFolderManagerAdapter)(nil) in folder_manager_test.go
// keep both packages in lockstep).
//
// W16-PR4 HISTORY — the prior Go 1.9+ type-alias pattern (see the
// June 2026 git history referencing `type DriveFileRef =
// drive.DriveFileRef`) existed to break what would have been an
// application→infrastructure→application cycle through the assetop
// middle layer. With the type alias removed the cycle is no longer
// reachable: the port signature in artlist.ports.go uses
// `[]drivepkg.DriveFileRef` qualified, which forces callers to
// import drivepkg directly. Method sets, struct access, and
// interface-style return values remain interchangeable because of
// the underlying struct identity (carried over from the alias era).
// DriveFileRef is intentionally minimal: just enough metadata for
// application-level consumers to identify a sibling file by name
// when iterating over listing results. The "Name" field is
// currently unused by the only consumer (semantic_enricher reads
// only .ID) but kept for future callers and to keep the adapter's
// Drive query fields self-documenting (`files(id, name)`).
package drive

// DriveFileRef is the canonical cross-package representation of a
// Google Drive file reference. Returned by DriveFolderManagerAdapter
// (and may be returned by any future Drive port). Application-layer
// callers reference this type via the `drivepkg` import alias
// (e.g. `drivepkg.DriveFileRef` from internal/app/* modules), as
// the prior `artlist.DriveFileRef` alias was removed in W16-PR4.
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
