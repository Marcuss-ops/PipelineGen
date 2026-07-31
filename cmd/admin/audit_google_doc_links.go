package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"

	"google.golang.org/api/docs/v1"
	"google.golang.org/api/googleapi"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
)

const auditDriveFolderMimeType = "application/vnd.google-apps.folder"

// fileMetaReader is the narrow read-only seam needed by the audit. The
// concrete Drive uploader satisfies this interface, while tests can inject a
// small deterministic fake without constructing OAuth clients.
type fileMetaReader interface {
	GetFileMeta(ctx context.Context, fileID string) (*drive.FileMeta, error)
}

type googleDocLinkAuditReport struct {
	DocumentID                string                    `json:"document_id"`
	DocumentDriveLinksTotal   int                       `json:"document_drive_links_total"`
	DocumentDriveLinksValid   int                       `json:"document_drive_links_valid"`
	DocumentDriveLinksInvalid int                       `json:"document_drive_links_invalid"`
	Links                     []googleDocLinkAuditEntry `json:"links,omitempty"`
}

type googleDocLinkAuditEntry struct {
	Href     string `json:"href"`
	FileID   string `json:"file_id,omitempty"`
	Status   string `json:"status"`
	Reason   string `json:"reason,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
}

type invalidGoogleDocLinksError struct {
	Count int
}

func (e *invalidGoogleDocLinksError) Error() string {
	return fmt.Sprintf("audit-google-doc-links: %d invalid Drive link(s)", e.Count)
}

type extractedGoogleDocHref struct {
	Href   string
	FileID string
	Err    error
}

// runAuditGoogleDocLinks retrieves one Google Doc and verifies every Drive
// href found in its content. It is read-only: no document, Drive file, or
// SQLite state is mutated.
func runAuditGoogleDocLinks(args []string) error {
	fs := flag.NewFlagSet("audit-google-doc-links", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	docID := fs.String("doc-id", "", "Google Doc ID to audit (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*docID) == "" {
		return fmt.Errorf("audit-google-doc-links: --doc-id is required")
	}
	*docID = strings.TrimSpace(*docID)

	cfg, log, cleanup, err := appLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	ctx := cmdContext()
	docClient, err := drive.NewDocClient(ctx, cfg.GetCredentialsPath(), cfg.GetTokenPath())
	if err != nil {
		return fmt.Errorf("audit-google-doc-links: initialize Google clients: %w", err)
	}
	documentReader, ok := docClient.(drive.DocumentReader)
	if !ok {
		return fmt.Errorf("audit-google-doc-links: configured document client cannot read document structure")
	}
	document, err := documentReader.GetDocument(ctx, *docID)
	if err != nil {
		return fmt.Errorf("audit-google-doc-links: retrieve document %s: %w", *docID, err)
	}

	uploader, err := buildDriveAdminForCLI(ctx, cfg, log)
	if err != nil {
		return fmt.Errorf("audit-google-doc-links: initialize Drive reader: %w", err)
	}
	report := auditGoogleDocLinks(ctx, *docID, document, uploader)
	return writeGoogleDocLinkAuditReport(os.Stdout, report)
}

func writeGoogleDocLinkAuditReport(w io.Writer, report googleDocLinkAuditReport) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		return fmt.Errorf("audit-google-doc-links: encode report: %w", err)
	}
	if report.DocumentDriveLinksInvalid > 0 {
		return &invalidGoogleDocLinksError{Count: report.DocumentDriveLinksInvalid}
	}
	return nil
}

func auditGoogleDocLinks(ctx context.Context, docID string, document *docs.Document, reader fileMetaReader) googleDocLinkAuditReport {
	report := googleDocLinkAuditReport{DocumentID: strings.TrimSpace(docID)}
	for _, href := range documentDriveHrefs(document) {
		report.DocumentDriveLinksTotal++
		entry := googleDocLinkAuditEntry{Href: href.Href, FileID: href.FileID}
		if href.Err != nil {
			entry.Status = "INVALID"
			entry.Reason = "MALFORMED_DRIVE_HREF"
			if strings.Contains(strings.ToLower(href.Err.Error()), "folder") {
				entry.Reason = "FOLDER_NOT_FILE"
			}
			report.DocumentDriveLinksInvalid++
			report.Links = append(report.Links, entry)
			continue
		}

		if reader == nil {
			entry.Status = "INVALID"
			entry.Reason = "DRIVE_READER_UNAVAILABLE"
			report.DocumentDriveLinksInvalid++
			report.Links = append(report.Links, entry)
			continue
		}
		meta, err := reader.GetFileMeta(ctx, href.FileID)
		if err != nil {
			entry.Status = "INVALID"
			entry.Reason = classifyDriveAuditError(err)
			report.DocumentDriveLinksInvalid++
			report.Links = append(report.Links, entry)
			continue
		}
		if meta == nil {
			entry.Status = "INVALID"
			entry.Reason = "EMPTY_DRIVE_METADATA"
			report.DocumentDriveLinksInvalid++
			report.Links = append(report.Links, entry)
			continue
		}
		entry.MimeType = strings.TrimSpace(meta.MimeType)
		switch {
		case strings.TrimSpace(meta.ID) != href.FileID:
			entry.Status, entry.Reason = "INVALID", "DRIVE_ID_MISMATCH"
		case meta.Trashed:
			entry.Status, entry.Reason = "INVALID", "TRASHED"
		case entry.MimeType == "":
			entry.Status, entry.Reason = "INVALID", "MIME_TYPE_MISSING"
		case entry.MimeType == auditDriveFolderMimeType:
			entry.Status, entry.Reason = "INVALID", "FOLDER_NOT_FILE"
		case strings.TrimSpace(meta.WebViewLink) == "":
			entry.Status, entry.Reason = "INVALID", "WEB_VIEW_LINK_MISSING"
		case driveLinkFileID(meta.WebViewLink) != href.FileID:
			entry.Status, entry.Reason = "INVALID", "WEB_VIEW_LINK_ID_MISMATCH"
		default:
			entry.Status = "VALID"
		}
		if entry.Status == "VALID" {
			report.DocumentDriveLinksValid++
		} else {
			report.DocumentDriveLinksInvalid++
		}
		report.Links = append(report.Links, entry)
	}
	return report
}

// documentDriveHrefs returns Drive href occurrences in deterministic order.
// A repeated href is intentionally retained as a repeated occurrence: the
// report describes every link in the document, not only unique file IDs.
//
// Docs API embedded/positioned objects do not expose an external href field
// in the structural types used here; linked charts/images are represented by
// resource references rather than clickable document hrefs. Clickable links
// are therefore collected from TextStyle.Link and RichLink only.
func documentDriveHrefs(document *docs.Document) []extractedGoogleDocHref {
	if document == nil {
		return nil
	}
	var hrefs []string
	if len(document.Tabs) > 0 {
		for _, tab := range document.Tabs {
			collectDocumentTabHrefs(tab, &hrefs)
		}
	} else {
		collectDocumentContentHrefs(document.Body, document.Headers, document.Footers, document.Footnotes, &hrefs)
	}
	sort.Strings(hrefs)
	result := make([]extractedGoogleDocHref, 0, len(hrefs))
	for _, href := range hrefs {
		if !isDriveHref(href) {
			continue
		}
		id, err := parseDriveHref(href)
		result = append(result, extractedGoogleDocHref{Href: href, FileID: id, Err: err})
	}
	return result
}

func collectDocumentTabHrefs(tab *docs.Tab, hrefs *[]string) {
	if tab == nil {
		return
	}
	if tab.DocumentTab != nil {
		content := tab.DocumentTab
		collectDocumentContentHrefs(content.Body, content.Headers, content.Footers, content.Footnotes, hrefs)
	}
	for _, child := range tab.ChildTabs {
		collectDocumentTabHrefs(child, hrefs)
	}
}

func collectDocumentContentHrefs(body *docs.Body, headers map[string]docs.Header, footers map[string]docs.Footer, footnotes map[string]docs.Footnote, hrefs *[]string) {
	if body != nil {
		collectStructuralHrefs(body.Content, hrefs)
	}
	for _, header := range headers {
		collectStructuralHrefs(header.Content, hrefs)
	}
	for _, footer := range footers {
		collectStructuralHrefs(footer.Content, hrefs)
	}
	for _, footnote := range footnotes {
		collectStructuralHrefs(footnote.Content, hrefs)
	}
}

func collectStructuralHrefs(elements []*docs.StructuralElement, hrefs *[]string) {
	for _, element := range elements {
		if element == nil {
			continue
		}
		if element.Paragraph != nil {
			for _, paragraphElement := range element.Paragraph.Elements {
				if paragraphElement == nil {
					continue
				}
				if textRun := paragraphElement.TextRun; textRun != nil && textRun.TextStyle != nil && textRun.TextStyle.Link != nil {
					if href := strings.TrimSpace(textRun.TextStyle.Link.Url); href != "" {
						*hrefs = append(*hrefs, href)
					}
				}
				if richLink := paragraphElement.RichLink; richLink != nil && richLink.RichLinkProperties != nil {
					if href := strings.TrimSpace(richLink.RichLinkProperties.Uri); href != "" {
						*hrefs = append(*hrefs, href)
					}
				}
			}
		}
		if element.Table != nil {
			for _, row := range element.Table.TableRows {
				if row == nil {
					continue
				}
				for _, cell := range row.TableCells {
					if cell != nil {
						collectStructuralHrefs(cell.Content, hrefs)
					}
				}
			}
		}
		if element.TableOfContents != nil {
			collectStructuralHrefs(element.TableOfContents.Content, hrefs)
		}
	}
}

func isDriveHref(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return host == "drive.google.com" || host == "www.drive.google.com" || host == "docs.google.com"
}

func parseDriveHref(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", err
	}
	host := strings.ToLower(parsed.Hostname())
	path := parsed.Path
	if host == "docs.google.com" && strings.HasPrefix(path, "/document/d/") {
		return pathSegmentID(strings.TrimPrefix(path, "/document/d/"))
	}
	if (host == "drive.google.com" || host == "www.drive.google.com") && strings.HasPrefix(path, "/file/d/") {
		return pathSegmentID(strings.TrimPrefix(path, "/file/d/"))
	}
	if (host == "drive.google.com" || host == "www.drive.google.com") && strings.HasPrefix(path, "/drive/folders/") {
		id, idErr := pathSegmentID(strings.TrimPrefix(path, "/drive/folders/"))
		if idErr != nil {
			return "", idErr
		}
		return id, errors.New("Drive URL points to a folder, not a file")
	}
	if (host == "drive.google.com" || host == "www.drive.google.com") && (path == "/uc" || path == "/open") {
		if id := strings.TrimSpace(parsed.Query().Get("id")); id != "" {
			return validateDriveID(id)
		}
	}
	return "", errors.New("unsupported Drive URL shape")
}

func pathSegmentID(path string) (string, error) {
	if idx := strings.IndexByte(path, '/'); idx >= 0 {
		path = path[:idx]
	}
	return validateDriveID(strings.TrimSpace(path))
}

func validateDriveID(id string) (string, error) {
	if id == "" {
		return "", errors.New("empty Drive file ID")
	}
	for _, r := range id {
		if !(r == '-' || r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return "", errors.New("invalid Drive file ID")
		}
	}
	return id, nil
}

func driveLinkFileID(raw string) string {
	if !isDriveHref(raw) {
		return ""
	}
	id, err := parseDriveHref(raw)
	if err != nil {
		return ""
	}
	return id
}

func classifyDriveAuditError(err error) string {
	var apiErr *googleapi.Error
	if errors.As(err, &apiErr) {
		switch apiErr.Code {
		case http.StatusNotFound:
			return "MISSING"
		case http.StatusForbidden, http.StatusUnauthorized:
			return "INACCESSIBLE"
		}
	}
	return "DRIVE_API_ERROR"
}
