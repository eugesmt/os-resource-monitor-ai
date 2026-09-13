package ai

import (
	"math"
	"testing"

	"taskmanager-ai/internal/domain"
)

func TestEstimatePromptIsUnicodeAwareAndComponentsSum(t *testing.T) {
	cases := []struct {
		name    string
		system  string
		message []domain.ChatMessage
	}{
		{name: "ascii", system: "system", message: []domain.ChatMessage{{Role: "user", Content: "old"}, {Role: "assistant", Content: "answer"}, {Role: "user", Content: "current"}}},
		{name: "cyrillic", system: "система", message: []domain.ChatMessage{{Role: "user", Content: "история"}, {Role: "user", Content: "текущий запрос"}}},
		{name: "empty", system: "", message: []domain.ChatMessage{{Role: "user", Content: ""}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			first := estimatePrompt(tc.system, tc.message)
			second := estimatePrompt(tc.system, tc.message)
			if first != second {
				t.Fatalf("estimate is not deterministic: %#v != %#v", first, second)
			}
			if first.ComponentTokenCount != "estimated" || first.HistoryTruncated {
				t.Fatalf("metadata = %#v", first)
			}
			if first.EstimatedInputTokens != first.SystemPromptTokens+first.HistoryTokens+first.CurrentRequestTokens {
				t.Fatalf("components do not sum: %#v", first)
			}
		})
	}
}

func TestUsageCostAndContextCalculations(t *testing.T) {
	input, output := 100, 25
	usage := domain.AICallUsage{InputTokens: &input, OutputTokens: &output, UsageAvailable: true}
	addCost(&usage, UsageConfig{InputPricePerMillionTokens: 2, OutputPricePerMillionTokens: 4, PricingConfigured: true})
	if usage.EstimatedCostUSD == nil || math.Abs(*usage.EstimatedCostUSD-0.0003) > 1e-12 {
		t.Fatalf("cost = %v, want 0.0003", usage.EstimatedCostUSD)
	}
	if contextStatus(90, UsageConfig{ContextWindowTokens: 100}) != "approaching_limit" || contextStatus(89, UsageConfig{ContextWindowTokens: 100}) != "within_limit" || contextStatus(90, UsageConfig{}) != "unknown" {
		t.Fatal("unexpected context status")
	}
	missing := domain.AICallUsage{InputTokens: &input, OutputTokens: &output}
	addCost(&missing, UsageConfig{InputPricePerMillionTokens: 2, OutputPricePerMillionTokens: 4})
	if missing.EstimatedCostUSD != nil {
		t.Fatal("cost should be unavailable without complete pricing config")
	}
}

func TestProviderUsageUnavailableAndInconsistent(t *testing.T) {
	base := domain.AICallUsage{ComponentTokenCount: "estimated"}
	empty := providerUsage(base, 0, 0, 0)
	if empty.UsageAvailable || !empty.UsageInconsistent || empty.InputTokens != nil {
		t.Fatalf("empty usage = %#v", empty)
	}
	inconsistent := providerUsage(base, 10, 2, 99)
	if inconsistent.UsageAvailable || !inconsistent.UsageInconsistent || *inconsistent.InputTokens != 10 || *inconsistent.TotalTokens != 99 {
		t.Fatalf("inconsistent usage = %#v", inconsistent)
	}
}
