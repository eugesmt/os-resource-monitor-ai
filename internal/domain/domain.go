package domain

type ChatMessage struct {
	ID      int64        `json:"id"`
	Role    string       `json:"role"`
	Content string       `json:"content"`
	Usage   *AICallUsage `json:"usage,omitempty"`
}

// AICallUsage contains provider-reported token counts and local prompt
// estimates for one successful model call.
type AICallUsage struct {
	Model                string   `json:"model,omitempty"`
	CurrentRequestTokens int      `json:"current_request_tokens"`
	HistoryTokens        int      `json:"history_tokens"`
	SystemPromptTokens   int      `json:"system_prompt_tokens"`
	EstimatedInputTokens int      `json:"estimated_input_tokens"`
	InputTokens          *int     `json:"input_tokens"`
	OutputTokens         *int     `json:"output_tokens"`
	TotalTokens          *int     `json:"total_tokens"`
	EstimatedCostUSD     *float64 `json:"estimated_cost_usd"`
	ComponentTokenCount  string   `json:"component_token_count"`
	UsageAvailable       bool     `json:"usage_available"`
	UsageInconsistent    bool     `json:"usage_inconsistent"`
	ContextStatus        string   `json:"context_status"`
	ContextWindowTokens  *int     `json:"context_window_tokens,omitempty"`
	HistoryTruncated     bool     `json:"history_truncated"`
}

const (
	ChatRoleUser      = "user"
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
