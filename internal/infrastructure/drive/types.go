// Package drive — types.go (PR2.7, F3.14 follow-up)
//
// Cross-package Drive value types. After F2.11 + F3.14 (June 2026)
// retired the artlist.DriveFolderManager wide port and the
// DriveFolderManagerAdapter's ListByQuery / Download / Upload dead-code
// surface, the only reason DriveFileRef existed (the PR2.7 cycle-break
// type shared between folder_manager.go and artlist) is gone. Per
// AGENTS.md Code Hygiene ("remove unused variables, functions, and
// files as a result of your changes"), the DriveFileRef type definition
// was deleted in this F3.14 follow-up commit; the only remaining type
// here is Doc, which is still actively produced by DocClient
// (Doc.Create / Doc.ListRecentDocs / Doc.Update).
//
// Cycle-break rationale (PR2.7, now historical only):
//
// \tartlist → ... → assets/assetop → drive → artlist  (cycle, rejected)
//
// PR2.7 placed the DriveFileRef struct in internal/infrastructure/drive
// (this file) so the folder_manager.go adapter could return a
// domain-shaped slice ([]DriveFileRef) without importing the artlist
// application package. The artlist package aliased the type via
// `type DriveFileRef = drive.DriveFileRef` (also deleted in F2.11).
// F2.11 + F3.14 closed the consumers (DestinationService →
// delivery.Publisher; SemanticEnricher → drive.Reader; etc.) per godlike/06
// 'one owner per fact', so the underlying type itself has no remaining
// direct callers and is retired.
package drive

// Doc is the canonical Google Docs reference returned by DocClient
// (internal/infrastructure/drive/doc_client.go). It bundles the IDs
// and human-facing metadata needed by callers (CreateDoc, ListRecentDocs,
// UpdateDoc) — content is populated only for newly created docs.
type Doc struct {
	ID            string
	Title         string
	URL           string
	Content       string
	ContentSHA256 string
	CreatedAt     string
}
