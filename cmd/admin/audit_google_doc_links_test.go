package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"google.golang.org/api/googleapi"

	"google.golang.org/api/docs/v1"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
)

type auditFileMetaReaderStub struct {
	files map[string]*drive.FileMeta
	errs  map[string]error
	calls []string
}

func (s *auditFileMetaReaderStub) GetFileMeta(_ context.Context, fileID string) (*drive.FileMeta, error) {
	s.calls = append(s.calls, fileID)
	if err := s.errs[fileID]; err != nil {
		return nil, err
	}
	return s.files[fileID], nil
}

func TestDocumentDriveHrefs_ExtractsNestedAndTabContent(t *testing.T) {
	doc := &docs.Document{
		Body: &docs.Body{Content: []*docs.StructuralElement{
			textLinkElement("https://drive.google.com/file/d/body-id/view"),
			tableLinkElement("https://drive.google.com/file/d/table-id/view"),
		}},
		Headers: map[string]docs.Header{
			"h": {Content: []*docs.StructuralElement{textLinkElement("https://drive.google.com/file/d/header-id/view")}},
		},
		Tabs: []*docs.Tab{{DocumentTab: &docs.DocumentTab{
			Body: &docs.Body{Content: []*docs.StructuralElement{textLinkElement("https://drive.google.com/file/d/tab-id/view")}},
		}}},
	}

	got := documentDriveHrefs(doc)
	if len(got) != 1 {
		t.Fatalf("tabs-enabled document must use tab content without duplicating legacy content: got %#v", got)
	}
	if got[0].FileID != "tab-id" {
		t.Fatalf("unexpected tab href extraction: %#v", got[0])
	}
}

func TestDocumentDriveHrefs_ExtractsLegacySectionsAndRichLinks(t *testing.T) {
	doc := &docs.Document{
		Body: &docs.Body{Content: []*docs.StructuralElement{
			textLinkElement("https://drive.google.com/file/d/body-id/view"),
			{Paragraph: &docs.Paragraph{Elements: []*docs.ParagraphElement{{
				RichLink: &docs.RichLink{RichLinkProperties: &docs.RichLinkProperties{
					Uri: "https://drive.google.com/open?id=rich-id",
				}},
			}}}},
		}},
		Headers: map[string]docs.Header{
			"h": {Content: []*docs.StructuralElement{textLinkElement("https://drive.google.com/file/d/header-id/view")}},
		},
		Footers: map[string]docs.Footer{
			"f": {Content: []*docs.StructuralElement{textLinkElement("https://drive.google.com/file/d/footer-id/view")}},
		},
		Footnotes: map[string]docs.Footnote{
			"n": {Content: []*docs.StructuralElement{textLinkElement("https://drive.google.com/file/d/footnote-id/view")}},
		},
	}

	got := documentDriveHrefs(doc)
	if len(got) != 5 {
		t.Fatalf("expected five Drive href occurrences, got %d (%#v)", len(got), got)
	}
}

func TestAuditGoogleDocLinks_ClassifiesDriveHTTPFailures(t *testing.T) {
	reader := &auditFileMetaReaderStub{errs: map[string]error{
		"missing-id":      &googleapi.Error{Code: http.StatusNotFound},
		"inaccessible-id": &googleapi.Error{Code: http.StatusForbidden},
	}}
	doc := &docs.Document{Body: &docs.Body{Content: []*docs.StructuralElement{
		textLinkElement("https://drive.google.com/file/d/missing-id/view"),
		textLinkElement("https://drive.google.com/file/d/inaccessible-id/view"),
	}}}

	report := auditGoogleDocLinks(context.Background(), "doc-http", doc, reader)
	if report.DocumentDriveLinksTotal != 2 || report.DocumentDriveLinksValid != 0 || report.DocumentDriveLinksInvalid != 2 {
		t.Fatalf("unexpected HTTP failure counters: %#v", report)
	}
	reasons := map[string]string{}
	for _, entry := range report.Links {
		reasons[entry.FileID] = entry.Reason
	}
	if reasons["missing-id"] != "MISSING" || reasons["inaccessible-id"] != "INACCESSIBLE" {
		t.Fatalf("HTTP failures were not classified fail-closed: %#v", report.Links)
	}
}

func TestWriteGoogleDocLinkAuditReport_EmitsJSONAndFailsClosed(t *testing.T) {
	var output bytes.Buffer
	err := writeGoogleDocLinkAuditReport(outputWriter{&output}, googleDocLinkAuditReport{
		DocumentID: "doc-1", DocumentDriveLinksTotal: 1, DocumentDriveLinksInvalid: 1,
		Links: []googleDocLinkAuditEntry{{Href: "https://drive.google.com/file/d/bad/view", Status: "INVALID"}},
	})
	if err == nil {
		t.Fatal("invalid report must return a non-nil error")
	}
	var decoded googleDocLinkAuditReport
	if decodeErr := json.Unmarshal(output.Bytes(), &decoded); decodeErr != nil {
		t.Fatalf("stdout must remain valid JSON: %v; output=%s", decodeErr, output.String())
	}
	if decoded.DocumentDriveLinksInvalid != 1 {
		t.Fatalf("decoded invalid count = %d, want 1", decoded.DocumentDriveLinksInvalid)
	}
}

type outputWriter struct{ buffer *bytes.Buffer }

func (w outputWriter) Write(p []byte) (int, error) { return w.buffer.Write(p) }

func TestInvalidGoogleDocLinksError_ReportsCount(t *testing.T) {
	err := &invalidGoogleDocLinksError{Count: 3}
	if got, want := err.Error(), "audit-google-doc-links: 3 invalid Drive link(s)"; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}

func TestAuditGoogleDocLinks_FailsClosedAndCountsOccurrences(t *testing.T) {
	reader := &auditFileMetaReaderStub{
		files: map[string]*drive.FileMeta{
			"valid-id":   {ID: "valid-id", MimeType: "video/mp4", WebViewLink: "https://drive.google.com/file/d/valid-id/view"},
			"trashed-id": {ID: "trashed-id", MimeType: "video/mp4", Trashed: true, WebViewLink: "https://drive.google.com/file/d/trashed-id/view"},
			"folder-id":  {ID: "folder-id", MimeType: auditDriveFolderMimeType, WebViewLink: "https://drive.google.com/file/d/folder-id/view"},
		},
		errs: map[string]error{"missing-id": errors.New("permission denied")},
	}
	doc := &docs.Document{Body: &docs.Body{Content: []*docs.StructuralElement{
		textLinkElement("https://drive.google.com/file/d/valid-id/view"),
		textLinkElement("https://drive.google.com/file/d/valid-id/view"),
		textLinkElement("https://drive.google.com/file/d/trashed-id/view"),
		textLinkElement("https://drive.google.com/file/d/folder-id/view"),
		textLinkElement("https://drive.google.com/file/d/missing-id/view"),
	}}}

	report := auditGoogleDocLinks(context.Background(), "doc-1", doc, reader)
	if report.DocumentDriveLinksTotal != 5 || report.DocumentDriveLinksValid != 2 || report.DocumentDriveLinksInvalid != 3 {
		t.Fatalf("unexpected counters: %#v", report)
	}
	if len(reader.calls) != 5 {
		t.Fatalf("each parsed occurrence must be verified, calls=%v", reader.calls)
	}
	folderFound := false
	for _, entry := range report.Links {
		if entry.FileID == "folder-id" {
			folderFound = true
			if entry.Reason != "FOLDER_NOT_FILE" {
				t.Fatalf("folder link must be invalid, entry=%#v", entry)
			}
		}
	}
	if !folderFound {
		t.Fatalf("folder audit entry missing: %#v", report.Links)
	}
}

func textLinkElement(href string) *docs.StructuralElement {
	return &docs.StructuralElement{Paragraph: &docs.Paragraph{Elements: []*docs.ParagraphElement{{
		TextRun: &docs.TextRun{TextStyle: &docs.TextStyle{Link: &docs.Link{Url: href}}},
	}}}}
}

func tableLinkElement(href string) *docs.StructuralElement {
	return &docs.StructuralElement{Table: &docs.Table{TableRows: []*docs.TableRow{{
		TableCells: []*docs.TableCell{{Content: []*docs.StructuralElement{textLinkElement(href)}}},
	}}}}
}
