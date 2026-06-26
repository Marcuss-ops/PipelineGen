// Package script — response.go defines the canonical response
// types for the unified script-generation endpoint. These are
// the typed output shapes that the HTTP handler serialises to
// JSON.
package script

// GenerateResponse is the canonical response for
// POST /api/script/generate. It carries the result of a
// single-item or multi-item generation request.
type GenerateResponse struct {
	OK bool `json:"ok"`

	// Async fields — populated when the job was enqueued (async path).
	JobID     string `json:"job_id,omitempty"`
	Status    string `json:"status,omitempty"`
	StatusURL string `json:"status_url,omitempty"`

	// Sync single-item fields — populated for synchronous single-item generation.
	Script      string `json:"script,omitempty"`
	WordCount   int    `json:"word_count,omitempty"`
	Title       string `json:"title,omitempty"`
	Language    string `json:"language,omitempty"`
	Model       string `json:"model,omitempty"`
	CacheStatus string `json:"cache_status,omitempty"`
	CacheHit    bool   `json:"cache_hit,omitempty"`

	// Sync multi-item fields — populated for synchronous batch generation.
	Count    int                `json:"count,omitempty"`
	Total    int                `json:"total,omitempty"`
	Results  []GenerateResponse `json:"results,omitempty"`

	// Postprocessor output.
	EntitiesJSON string `json:"entities_json,omitempty"`
	DocLink      string `json:"doc_link,omitempty"`
	DocID        string `json:"doc_id,omitempty"`

	// Meta.
	DocTitle string   `json:"doc_title,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// async generates a response for the async (job-enqueued) branch.
func (r *GenerateResponse) async(jobID, status, statusURL, docTitle string) {
	r.OK = true
	r.JobID = jobID
	r.Status = status
	r.StatusURL = statusURL
	r.DocTitle = docTitle
}

// syncSingle populates the response with a single-item sync result.
func (r *GenerateResponse) syncSingle(script string, wordCount int, title, lang, model, cacheStatus string, cacheHit bool) {
	r.OK = true
	r.Script = script
	r.WordCount = wordCount
	r.Title = title
	r.Language = lang
	r.Model = model
	r.CacheStatus = cacheStatus
	r.CacheHit = cacheHit
}

// syncMulti populates the response with multi-item sync results.
func (r *GenerateResponse) syncMulti(count, total int, results []GenerateResponse) {
	r.OK = true
	r.Count = count
	r.Total = total
	r.Results = results
}
