package clips

import "encoding/json"

// jsonUnmarshal is a thin indirection above encoding/json so callers in
// the application/clips package can keep their internal helper logic
// (bulk_upload_worker.go, upload_helpers.go) without importing the
// encoding/json package in every helper. The actual defaulting lives
// here once for the whole package.
func jsonUnmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}
