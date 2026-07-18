package main

import (
	"embed"
	"fmt"
	"html/template"
	"strings"
	"time"
)

//go:embed templates/*.html
var templatesFS embed.FS

func loadTemplates() *template.Template {
	tmpl := template.New("operator-console")
	tmpl = tmpl.Funcs(template.FuncMap{
		"safeHTML": func(s string) template.HTML {
			return template.HTML(s)
		},
		"formatTime": func(t time.Time) string {
			if t.IsZero() {
				return "-"
			}
			return t.In(time.Local).Format("2006-01-02 15:04")
		},
		"formatBytes": func(b int64) string {
			const unit = 1024
			if b < unit {
				return fmt.Sprintf("%d B", b)
			}
			div, exp := int64(unit), 0
			for n := b / unit; n >= unit; n /= unit {
				div *= unit
				exp++
			}
			return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
		},
		"lifecycleBadgeClass": func(state string) string {
			switch state {
			case "ready", "published":
				return "badge-success"
			case "processing":
				return "badge-warning"
			case "created":
				return "badge-info"
			case "archived":
				return "badge-muted"
			default:
				return "badge-muted"
			}
		},
		"processingBadgeClass": func(status string) string {
			switch status {
			case "completed":
				return "badge-success"
			case "running":
				return "badge-warning"
			case "failed":
				return "badge-danger"
			default:
				return "badge-muted"
			}
		},
		"jobStatusBadgeClass": func(status string) string {
			switch strings.ToLower(status) {
			case "succeeded":
				return "badge-success"
			case "running", "queued":
				return "badge-info"
			case "failed":
				return "badge-danger"
			case "cancelled":
				return "badge-muted"
			case "retry_wait":
				return "badge-warning"
			default:
				return "badge-muted"
			}
		},
		"outboxStatusBadgeClass": func(status string) string {
			switch strings.ToLower(status) {
			case "completed", "succeeded":
				return "badge-success"
			case "processing", "running", "pending":
				return "badge-warning"
			case "dead_letter", "failed":
				return "badge-danger"
			default:
				return "badge-muted"
			}
		},
		"toString": func(v interface{}) string {
			return fmt.Sprintf("%v", v)
		},
		"filterQueryParams": func(f AssetFilter) string {
			var parts []string
			if f.Source != "" {
				parts = append(parts, "source="+f.Source)
			}
			if f.MediaType != "" {
				parts = append(parts, "media_type="+f.MediaType)
			}
			if f.LifecycleState != "" {
				parts = append(parts, "lifecycle_state="+f.LifecycleState)
			}
			if f.Q != "" {
				parts = append(parts, "q="+f.Q)
			}
			return strings.Join(parts, "&")
		},
	})

	tmpl, err := tmpl.ParseFS(templatesFS, "templates/*.html")
	if err != nil {
		panic(err)
	}
	return tmpl
}
