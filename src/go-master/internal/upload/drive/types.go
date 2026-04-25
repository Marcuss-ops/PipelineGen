// Package drive provides types for Google Docs integration.
package drive

// Doc represents a Google Docs document
type Doc struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	URL     string `json:"url"`
	Content string `json:"content,omitempty"`
}
