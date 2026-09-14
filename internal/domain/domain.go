package domain

type ContextStrategyName string

const (
	ContextStrategySlidingWindow ContextStrategyName = "sliding_window"
	ContextStrategyStickyFacts   ContextStrategyName = "sticky_facts"
	ContextStrategyBranching     ContextStrategyName = "branching"
)

type ContextScope struct {
	ConversationID int64 `json:"conversation_id"`
	BranchID       int64 `json:"branch_id"`
	Version        int64 `json:"version"`
}

type DialogueBranch struct {
	ID             int64  `json:"id"`
	ConversationID int64  `json:"conversation_id"`
	Name           string `json:"name,omitempty"`
	ParentBranchID int64  `json:"parent_branch_id,omitempty"`
	CheckpointID   int64  `json:"checkpoint_id,omitempty"`
	CreatedAt      string `json:"created_at"`
}

type DialogueCheckpoint struct {
	ID             int64  `json:"id"`
	ConversationID int64  `json:"conversation_id"`
	BranchID       int64  `json:"branch_id"`
	MessageID      int64  `json:"message_id"`
	Label          string `json:"label,omitempty"`
	CreatedAt      string `json:"created_at"`
}

type DialogueFact struct {
	ID                  int64  `json:"id"`
	ConversationID      int64  `json:"conversation_id"`
	BranchID            int64  `json:"branch_id"`
	FactKey             string `json:"fact_key"`
	Category            string `json:"category"`
	ValueText           string `json:"value_text,omitempty"`
	State               string `json:"state"`
	SourceMessageID     *int64 `json:"source_message_id,omitempty"`
	Version             int64  `json:"version"`
	UpdatedAt           string `json:"updated_at"`
	ExpiresAt           string `json:"expires_at,omitempty"`
	InheritedFromFactID *int64 `json:"inherited_from_fact_id,omitempty"`
}

type FactMutation struct {
	Operation       string `json:"operation"`
	Key             string `json:"key"`
	Category        string `json:"category"`
	Value           string `json:"value,omitempty"`
	ExpectedVersion *int64 `json:"expected_version,omitempty"`
	Reason          string `json:"reason,omitempty"`
}

type ChatMessage struct {
	ID             int64        `json:"id"`
	Role           string       `json:"role"`
	Content        string       `json:"content"`
	Usage          *AICallUsage `json:"usage,omitempty"`
	ConversationID int64        `json:"conversation_id,omitempty"`
	BranchID       int64        `json:"branch_id,omitempty"`
}

// AICallUsage contains provider-reported token counts and local prompt
// estimates for one successful model call.
type AICallUsage struct {
	ContextStrategy                  ContextStrategyName `json:"context_strategy,omitempty"`
	BranchID                         int64               `json:"branch_id,omitempty"`
	ProviderMessageCount             int                 `json:"provider_message_count"`
	HistoryMessageCount              int                 `json:"history_message_count"`
	ServiceContextTokens             int                 `json:"service_context_tokens"`
	FactsCount                       int                 `json:"facts_count"`
	FactsTokens                      int                 `json:"facts_tokens"`
	BranchMetadataTokens             int                 `json:"branch_metadata_tokens"`
	AuxiliaryInputTokens             *int                `json:"auxiliary_input_tokens,omitempty"`
	AuxiliaryOutputTokens            *int                `json:"auxiliary_output_tokens,omitempty"`
	AuxiliaryTotalTokens             *int                `json:"auxiliary_total_tokens,omitempty"`
	AuxiliaryCostUSD                 *float64            `json:"auxiliary_cost_usd,omitempty"`
	AggregateInputTokens             *int                `json:"aggregate_input_tokens,omitempty"`
	AggregateOutputTokens            *int                `json:"aggregate_output_tokens,omitempty"`
	AggregateTotalTokens             *int                `json:"aggregate_total_tokens,omitempty"`
	AggregateCostUSD                 *float64            `json:"aggregate_cost_usd,omitempty"`
	Model                            string              `json:"model,omitempty"`
	CurrentRequestTokens             int                 `json:"current_request_tokens"`
	HistoryTokens                    int                 `json:"history_tokens"`
	SystemPromptTokens               int                 `json:"system_prompt_tokens"`
	EstimatedInputTokens             int                 `json:"estimated_input_tokens"`
	InputTokens                      *int                `json:"input_tokens"`
	OutputTokens                     *int                `json:"output_tokens"`
	TotalTokens                      *int                `json:"total_tokens"`
	EstimatedCostUSD                 *float64            `json:"estimated_cost_usd"`
	ComponentTokenCount              string              `json:"component_token_count"`
	UsageAvailable                   bool                `json:"usage_available"`
	UsageInconsistent                bool                `json:"usage_inconsistent"`
	ContextStatus                    string              `json:"context_status"`
	ContextWindowTokens              *int                `json:"context_window_tokens,omitempty"`
	HistoryTruncated                 bool                `json:"history_truncated"`
	SummaryTokens                    int                 `json:"summary_tokens"`
	RecentHistoryTokens              int                 `json:"recent_history_tokens"`
	UncompressedHistoryTokens        int                 `json:"uncompressed_history_tokens"`
	EstimatedUncompressedInputTokens int                 `json:"estimated_uncompressed_input_tokens"`
	EstimatedTokensSaved             int                 `json:"estimated_tokens_saved"`
	HistoryCompacted                 bool                `json:"history_compacted"`
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
