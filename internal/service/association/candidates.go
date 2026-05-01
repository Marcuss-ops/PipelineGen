package association

import (
	"strings"
	"velox/go-master/pkg/sliceutil"
)

type CandidatesRequest struct {
	Topic      string   `json:"topic"`
	SegmentKey string   `json:"segment_key,omitempty"`
	Timestamp  string   `json:"timestamp,omitempty"`
	Subject    string   `json:"subject"`
	Narrative  string   `json:"narrative,omitempty"`
	Keywords   []string `json:"keywords,omitempty"`
	Entities   []string `json:"entities,omitempty"`
	TopK       int      `json:"top_k,omitempty"`
}

type Candidate struct {
	Database string `json:"database"`
	Source   string `json:"source"`
	Name     string `json:"name"`
	Path     string `json:"path,omitempty"`
	FolderID string `json:"folder_id,omitempty"`
	Link     string `json:"link,omitempty"`
	Score    int    `json:"score"`
	Reason   string `json:"reason,omitempty"`
}

type CandidatesResponse struct {
	OK         bool        `json:"ok"`
	Topic      string      `json:"topic,omitempty"`
	SegmentKey string      `json:"segment_key,omitempty"`
	Timestamp  string      `json:"timestamp,omitempty"`
	Subject    string      `json:"subject,omitempty"`
	TopK       int         `json:"top_k,omitempty"`
	Candidates []Candidate `json:"candidates,omitempty"`
	Error      string      `json:"error,omitempty"`
}

func (r *CandidatesRequest) Normalize() {
	if r.TopK <= 0 {
		r.TopK = 10
	}
	r.Topic = strings.TrimSpace(r.Topic)
	r.SegmentKey = strings.TrimSpace(r.SegmentKey)
	r.Timestamp = strings.TrimSpace(r.Timestamp)
	r.Subject = strings.TrimSpace(r.Subject)
	r.Narrative = strings.TrimSpace(r.Narrative)
	r.Keywords = sliceutil.UniqueStrings(sliceutil.TrimStrings(r.Keywords))
	r.Entities = sliceutil.UniqueStrings(sliceutil.TrimStrings(r.Entities))
}
