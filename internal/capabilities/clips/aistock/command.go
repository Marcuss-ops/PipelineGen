// Package aistock — typed command for AI-generated stock clip ingestion.
package clips

// CreateAIStockCommand is the input for creating a new AI-generated stock clip
// from a visual analysis document and a Google Drive video reference.
type CreateAIStockCommand struct {
	// DocumentJSON is the raw ai_stock_visual_analysis.v1 JSON document.
	DocumentJSON string
	// DriveURL is the Google Drive URL or raw file ID of the video file.
	DriveURL string
}
