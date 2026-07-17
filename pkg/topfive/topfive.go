// Package topfive canonicalizes short commentary ranking inputs.
package topfive

import (
	"fmt"
	"sort"
	"strings"
)

const SchemaVersion = "remotion.ranking.v1"

type Request struct {
	ID    string   `json:"id"`
	Title string   `json:"title,omitempty"`
	Items []Moment `json:"items"`
}

type Moment struct {
	Name       string  `json:"name"`
	ClipID     string  `json:"clip_id,omitempty"`
	Path       string  `json:"path"`
	StartMs    int64   `json:"start_ms"`
	EndMs      int64   `json:"end_ms"`
	Score      float64 `json:"score,omitempty"`
}

type Response struct {
	SchemaVersion string   `json:"schema_version"`
	Format        string   `json:"format"`
	ID            string   `json:"id"`
	Title         string   `json:"title"`
	Items         []Moment `json:"items"`
}

func Build(req Request) (Response, error) {
	if strings.TrimSpace(req.ID) == "" {
		return Response{}, fmt.Errorf("topfive: id is required")
	}
	if len(req.Items) == 0 || len(req.Items) > 5 {
		return Response{}, fmt.Errorf("topfive: between 1 and 5 moments are required")
	}
	items := append([]Moment(nil), req.Items...)
	for i := range items {
		items[i].Name = canonicalName(items[i].Name)
		if items[i].Name == "" || strings.TrimSpace(items[i].Path) == "" {
			return Response{}, fmt.Errorf("topfive: items[%d] name and path are required", i)
		}
		if items[i].StartMs < 0 || items[i].EndMs <= items[i].StartMs {
			return Response{}, fmt.Errorf("topfive: items[%d] has an invalid time window", i)
		}
		if items[i].EndMs-items[i].StartMs > 3000 {
			return Response{}, fmt.Errorf("topfive: items[%d] exceeds the 3 second limit", i)
		}
	}
	// Score is optional: when supplied, highest-scoring moments become the
	// ranking. Equal scores preserve the caller's order.
	sort.SliceStable(items, func(i, j int) bool { return items[i].Score > items[j].Score })
	if req.Title == "" {
		req.Title = "TOP 5 MOMENTS"
	}
	return Response{SchemaVersion: SchemaVersion, Format: "top-five-commentary", ID: req.ID, Title: req.Title, Items: items}, nil
}

func canonicalName(value string) string {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return ""
	}
	return strings.ToUpper(fields[0])
}
