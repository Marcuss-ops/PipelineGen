// Package nlp provides natural language processing functionality.
package nlp

// Moment rappresenta un momento chiave nel video
type Moment struct {
	StartTime  float64 `json:"start_time"`
	EndTime    float64 `json:"end_time"`
	Text       string  `json:"text"`
	Score      float64 `json:"score"`
	Importance string  `json:"importance"` // high, medium, low
}

// Keyword rappresenta una keyword estratta
type Keyword struct {
	Word  string  `json:"word"`
	Score float64 `json:"score"`
	Count int     `json:"count"`
}

// Segment rappresenta un segmento VTT
type Segment struct {
	Start float64
	End   float64
	Text  string
	Score float64
}

// VTT rappresenta un file WebVTT
type VTT struct {
	Segments []Segment
}