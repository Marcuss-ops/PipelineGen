package artlist

type ImportScraperDBRequest struct {
	DBPath string `json:"db_path"`
}

type ImportScraperDBResponse struct {
	OK      bool   `json:"ok"`
	Imported int    `json:"imported"`
	Error    string `json:"error,omitempty"`
}
