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
	Model                            string   `json:"model,omitempty"`
	CurrentRequestTokens             int      `json:"current_request_tokens"`
	HistoryTokens                    int      `json:"history_tokens"`
	SystemPromptTokens               int      `json:"system_prompt_tokens"`
	EstimatedInputTokens             int      `json:"estimated_input_tokens"`
	InputTokens                      *int     `json:"input_tokens"`
	OutputTokens                     *int     `json:"output_tokens"`
	TotalTokens                      *int     `json:"total_tokens"`
	EstimatedCostUSD                 *float64 `json:"estimated_cost_usd"`
	ComponentTokenCount              string   `json:"component_token_count"`
	UsageAvailable                   bool     `json:"usage_available"`
	UsageInconsistent                bool     `json:"usage_inconsistent"`
	ContextStatus                    string   `json:"context_status"`
	ContextWindowTokens              *int     `json:"context_window_tokens,omitempty"`
	HistoryTruncated                 bool     `json:"history_truncated"`
	SummaryTokens                    int      `json:"summary_tokens"`
	RecentHistoryTokens              int      `json:"recent_history_tokens"`
	UncompressedHistoryTokens        int      `json:"uncompressed_history_tokens"`
	EstimatedUncompressedInputTokens int      `json:"estimated_uncompressed_input_tokens"`
	EstimatedTokensSaved             int      `json:"estimated_tokens_saved"`
	HistoryCompacted                 bool     `json:"history_compacted"`
}

type DialogueSummary struct {
	Content               string `json:"content"`
	LastMessageID         int64  `json:"last_message_id"`
	UpdatedAt             string `json:"updated_at"`
	SourceMessageCount    int    `json:"source_message_count"`
	SourceTokensEstimate  int    `json:"source_tokens_estimate"`
	SummaryTokensEstimate int    `json:"summary_tokens_estimate"`
	RecentMessagesLimit   int    `json:"recent_messages_limit"`
	Version               int64  `json:"version"`
}

type SummaryCallUsage struct {
	StartedAt             string
	FinishedAt            string
	PreviousLastMessageID int64
	ThroughMessageID      int64
	MessageCount          int
	Model                 string
	Outcome               string
	PreviousSummaryTokens int
	NewMessagesTokens     int
	EstimatedInputTokens  int
	InputTokens           *int
	OutputTokens          *int
	TotalTokens           *int
	ResultSummaryTokens   int
	SummarySaved          bool
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
