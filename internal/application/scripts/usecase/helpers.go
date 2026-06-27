package usecase

// ScriptListFilter is the canonical filter for listing scripts.
type ScriptListFilter struct {
	ChannelID string `json:"channel_id"`
	Status    string `json:"status"`
	Language  string `json:"language"`
	Limit     int    `json:"limit"`
	Offset    int    `json:"offset"`
}
