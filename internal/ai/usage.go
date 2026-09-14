package ai

import (
	"sync/atomic"
	"unicode/utf8"

	"taskmanager-ai/internal/domain"
)

// UsageConfig contains optional, model-specific metrics configuration. Zero
// context size means that context utilization is unknown. PricingConfigured
// distinguishes an explicitly configured zero price from missing pricing.
type UsageConfig struct {
	ContextWindowTokens         int
	InputPricePerMillionTokens  float64
	OutputPricePerMillionTokens float64
	PricingConfigured           bool
}

type CompletionResult struct {
	Content string
	Usage   domain.AICallUsage
}

var callSequence atomic.Uint64

func estimateTokens(role, content string) int {
	// This is intentionally a stable, Unicode-aware approximation rather than
	// a claim about the provider tokenizer. The fixed framing covers role and
	// message separators.
	runes := utf8.RuneCountInString(role) + utf8.RuneCountInString(content)
	return 4 + (runes+3)/4
}

func estimatePrompt(systemPrompt string, messages []domain.ChatMessage) domain.AICallUsage {
	usage := domain.AICallUsage{
		ComponentTokenCount: "estimated",
		HistoryTruncated:    false,
	}
	usage.SystemPromptTokens = estimateTokens("system", systemPrompt)
	if len(messages) > 0 {
		for _, message := range messages[:len(messages)-1] {
			usage.HistoryTokens += estimateTokens(message.Role, message.Content)
		}
		last := messages[len(messages)-1]
		usage.CurrentRequestTokens = estimateTokens(last.Role, last.Content)
	}
	usage.EstimatedInputTokens = usage.SystemPromptTokens + usage.HistoryTokens + usage.CurrentRequestTokens
	return usage
}

func providerUsage(usage domain.AICallUsage, prompt, completion, total int) domain.AICallUsage {
	usage.InputTokens = intPtr(prompt)
	usage.OutputTokens = intPtr(completion)
	usage.TotalTokens = intPtr(total)
	if prompt == 0 && completion == 0 && total == 0 {
		usage.InputTokens = nil
		usage.OutputTokens = nil
		usage.TotalTokens = nil
		usage.UsageAvailable = false
		usage.UsageInconsistent = true
		return usage
	}
	usage.UsageInconsistent = total != prompt+completion
	usage.UsageAvailable = !usage.UsageInconsistent
	return usage
}

func addCost(usage *domain.AICallUsage, config UsageConfig) {
	if !config.PricingConfigured || !usage.UsageAvailable || usage.InputTokens == nil || usage.OutputTokens == nil {
		return
	}
	cost := float64(*usage.InputTokens)*config.InputPricePerMillionTokens/1_000_000 + float64(*usage.OutputTokens)*config.OutputPricePerMillionTokens/1_000_000
	usage.EstimatedCostUSD = &cost
}

func contextStatus(estimatedInput int, config UsageConfig) string {
	if config.ContextWindowTokens <= 0 {
		return "unknown"
	}
	if float64(estimatedInput) >= 0.9*float64(config.ContextWindowTokens) {
		return "approaching_limit"
	}
	return "within_limit"
}

func contextStatusForProvider(input int, config UsageConfig) string {
	if config.ContextWindowTokens <= 0 {
		return "unknown"
	}
	if float64(input) >= 0.9*float64(config.ContextWindowTokens) {
		return "approaching_limit"
	}
	return "within_limit"
}

func intPtr(v int) *int           { return &v }
func floatPtr(v float64) *float64 { return &v }

func usageCallID() uint64 { return callSequence.Add(1) }
