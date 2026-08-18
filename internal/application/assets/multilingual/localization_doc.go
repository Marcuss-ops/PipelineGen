package multilingual

import (
	"sort"
	"strings"
)

// localization_doc.go — the localization manifest Google Doc. The doc lists
// one entry per language in REQUESTED order (source=0, targets=1..N), never in
// render completion order: even if ES finishes before EN, the doc must show
// English before Spanish. The order guarantee lives here as a pure, tested
// function so no worker/queue reordering can silently flip the manifest.

// LocalizationDocEntry is one ordered row in the localization manifest doc.
type LocalizationDocEntry struct {
	Priority  int    `json:"priority"`
	Language  string `json:"language"`
	DriveLink string `json:"drive_link,omitempty"`
	Status    string `json:"status"`
}

// LocalizationDocRef identifies the published localization manifest doc and
// its ordered entries.
type LocalizationDocRef struct {
	ID      string                 `json:"id"`
	Link    string                 `json:"link"`
	Entries []LocalizationDocEntry `json:"entries"`
}

// AssembleLocalizationEntries orders language variants by the REQUESTED
// priority (source=0, targets=1..N), never by completion order. A stable sort
// keeps submission order for equal priorities. Variants without a Drive link
// are still listed (with an empty link + their status) so the requested order
// is preserved even when one language failed.
func AssembleLocalizationEntries(variants []VariantResult) []LocalizationDocEntry {
	entries := make([]LocalizationDocEntry, 0, len(variants))
	for _, v := range variants {
		entries = append(entries, LocalizationDocEntry{
			Priority:  v.Priority,
			Language:  v.Language,
			DriveLink: v.DriveLink,
			Status:    v.Status,
		})
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Priority < entries[j].Priority })
	return entries
}

// languageNames maps a BCP-47 code to its English display name (the configured
// multilingual language set). Unknown codes fall back to the raw code.
var languageNames = map[string]string{
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

// LanguageLabel returns the human display name for a BCP-47 code, falling back
// to the code itself when unmapped.
func LanguageLabel(code string) string {
	if n, ok := languageNames[code]; ok {
		return n
	}
	return code
}

// RenderLocalizationDoc renders the manifest as HTML: one heading per language
// in REQUESTED order, each carrying its MP4 Drive link. The output is
// deterministic and independent of render completion order.
func RenderLocalizationDoc(title string, entries []LocalizationDocEntry) string {
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
			sb.WriteString(" MP4</a></p>\n")
		} else {
			sb.WriteString("<p>")
			sb.WriteString(htmlEscapeText(e.Language))
			sb.WriteString(" MP4: ")
			sb.WriteString(htmlEscapeText(e.Status))
			sb.WriteString("</p>\n")
		}
	}
	return sb.String()
}

func htmlEscapeText(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}
