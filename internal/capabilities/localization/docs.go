package localization

// docs.go owns the localization Google Docs assembly: it takes the certified
// []LocalizedDocumentEntry, orders them by the requested priority, renders a
// deterministic HTML manifest, and publishes it through a narrow port.
//
// godlike/06 SSOT (one canonical owner per fact): the Docs writer applies ONE
// ordering — by Priority — then renders + publishes. It never learns about
// Whisper, Rust, FFmpeg, or the translation provider: those layers produced
// the certified facts that land in LocalizedDocumentEntry, and the assembler
// only renders them.

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// LocalizedDocumentRef identifies the published localization manifest doc and
// its priority-ordered entries. ID / Link are empty when publication did not
// happen (no publisher wired, or the publish call failed).
type LocalizedDocumentRef struct {
	ID      string
	Link    string
	Entries []LocalizedDocumentEntry
}

// DocPublishInput is the fully-resolved input for publishing the rendered
// doc. Content is the deterministic HTML produced by RenderLocalizedDocument.
type DocPublishInput struct {
	Title          string
	Content        string
	FolderID       string
	IdempotencyKey string
	Force          bool
}

// DocPublishResult is the published doc identity.
type DocPublishResult struct {
	ID   string
	Link string
}

// DocPublisher publishes a rendered localization doc (idempotently). The
// concrete adapter wraps the Drive DocClient; the capability never imports
// the Drive/SQLite stack.
type DocPublisher interface {
	Publish(ctx context.Context, in DocPublishInput) (*DocPublishResult, error)
}

// AssembleInput is the resolved input for the doc assembly. Entries may be in
// any order — the assembler orders them by Priority (stable).
type AssembleInput struct {
	Title          string
	FolderID       string
	IdempotencyKey string
	Force          bool
	Entries        []LocalizedDocumentEntry
}

// DocumentAssembler is the canonical Docs writer. It is immutable after
// construction and safe for concurrent Assemble calls.
type DocumentAssembler struct {
	publisher DocPublisher
}

// NewDocumentAssembler builds the assembler. Fail-closed: a nil publisher is
// rejected at construction (an assembler with no publish path could only
// render, never publish the doc).
func NewDocumentAssembler(publisher DocPublisher) (*DocumentAssembler, error) {
	if publisher == nil {
		return nil, fmt.Errorf("localization.NewDocumentAssembler: doc publisher is required")
	}
	return &DocumentAssembler{publisher: publisher}, nil
}

// Assemble orders the entries by priority, renders the manifest HTML, and
// publishes it. On a publish failure the returned ref still carries the
// ordered entries (the requested order survives offline), while the error
// carries the publish failure — the doc link is never fabricated.
func (a *DocumentAssembler) Assemble(ctx context.Context, in AssembleInput) (*LocalizedDocumentRef, error) {
	if a == nil || a.publisher == nil {
		return nil, fmt.Errorf("localization: document assembler is not initialized")
	}
	entries := append([]LocalizedDocumentEntry(nil), in.Entries...)
	SortLocalizedDocumentEntries(entries)

	ref := &LocalizedDocumentRef{Entries: entries}
	published, err := a.publisher.Publish(ctx, DocPublishInput{
		Title:          in.Title,
		Content:        RenderLocalizedDocument(in.Title, entries),
		FolderID:       in.FolderID,
		IdempotencyKey: in.IdempotencyKey,
		Force:          in.Force,
	})
	if err != nil {
		return ref, err
	}
	if published != nil {
		ref.ID = published.ID
		ref.Link = published.Link
	}
	return ref, nil
}

// RenderLocalizedDocument renders the manifest as deterministic HTML: one
// heading per language in priority order, each carrying its Drive link and
// duration. The output is independent of render completion order (the
// assembler orders before rendering).
func RenderLocalizedDocument(title string, entries []LocalizedDocumentEntry) string {
	var sb strings.Builder
	sb.WriteString("<h1>")
	sb.WriteString(htmlEscapeText(title))
	sb.WriteString("</h1>\n")
	for _, e := range entries {
		sb.WriteString("<h2>")
		sb.WriteString(LanguageLabel(e.Language))
		sb.WriteString("</h2>\n")
		if e.DriveLink != "" {
			sb.WriteString(`<p><a href="`)
			sb.WriteString(e.DriveLink)
			sb.WriteString(`">`)
			sb.WriteString(htmlEscapeText(e.Language))
			sb.WriteString(" MP4</a>")
		} else {
			sb.WriteString("<p>")
			sb.WriteString(htmlEscapeText(e.Language))
			sb.WriteString(" MP4: no link")
		}
		if e.DurationMS > 0 {
			sb.WriteString(" (")
			sb.WriteString(strconv.FormatInt(e.DurationMS, 10))
			sb.WriteString(" ms)")
		}
		sb.WriteString("</p>\n")
	}
	return sb.String()
}

// languageLabels maps a BCP-47 code to its English display name (the
// configured multilingual set). Unknown codes fall back to the raw code.
var languageLabels = map[string]string{
	"en":    "English",
	"es":    "Spanish",
	"it":    "Italian",
	"pt":    "Portuguese",
	"pt-BR": "Portuguese (Brazil)",
	"fr":    "French",
	"de":    "German",
	"nl":    "Dutch",
	"pl":    "Polish",
	"ro":    "Romanian",
	"ru":    "Russian",
	"tr":    "Turkish",
	"id":    "Indonesian",
}

// LanguageLabel returns the human display name for a BCP-47 code, falling
// back to the code itself when unmapped.
func LanguageLabel(code string) string {
	if n, ok := languageLabels[code]; ok {
		return n
	}
	return code
}

func htmlEscapeText(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}
