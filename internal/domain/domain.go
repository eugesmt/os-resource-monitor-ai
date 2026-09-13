package domain

type ChatMessage struct {
	ID int64 `json:"id"`
	Role string `json:"role"`
	Content string `json:"content"`
}

const (
	ChatRoleUser = "user"
	ChatRoleAssistant = "assistant"
)

type AnalysisResponse struct {
	Summary         string   `json:"summary"`
	Facts           []Fact   `json:"facts"`
	Recommendations []string `json:"recommendations"`
}

type Fact struct {
	Metric string  `json:"metric"`
	Value  float64 `json:"value"`
	Unit   string  `json:"unit"`
}
