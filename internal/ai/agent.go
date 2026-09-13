package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	"taskmanager-ai/internal/domain"
)

const (
	maxAIResponseBytes     = 32 << 10
	maxSummaryRunes        = 2000
	maxFactFieldRunes      = 200
	maxRecommendationRunes = 500
	maxFacts               = 5
	maxRecommendations     = 5
)

func validateAnalysisResponse(result domain.AnalysisResponse, responseBytes int) error {
	if responseBytes > maxAIResponseBytes || strings.TrimSpace(result.Summary) == "" || result.Facts == nil || result.Recommendations == nil {
		return fmt.Errorf("%w: missing or oversized required fields", ErrInvalidResponse)
	}
	if len(result.Facts) > maxFacts || len(result.Recommendations) > maxRecommendations {
		return fmt.Errorf("%w: too many facts or recommendations", ErrInvalidResponse)
	}
	if len([]rune(result.Summary)) > maxSummaryRunes {
		return fmt.Errorf("%w: summary is too long", ErrInvalidResponse)
	}
	for _, fact := range result.Facts {
		if strings.TrimSpace(fact.Metric) == "" || len([]rune(fact.Metric)) > maxFactFieldRunes || len([]rune(fact.Unit)) > maxFactFieldRunes || math.IsNaN(fact.Value) || math.IsInf(fact.Value, 0) {
			return fmt.Errorf("%w: invalid fact", ErrInvalidResponse)
		}
	}
	for _, recommendation := range result.Recommendations {
		if strings.TrimSpace(recommendation) == "" || len([]rune(recommendation)) > maxRecommendationRunes {
			return fmt.Errorf("%w: invalid recommendation", ErrInvalidResponse)
		}
	}
	return nil
}

type completion interface {
	Complete(ctx context.Context, systemPrompt, userContent string) (string, error)
}

type Agent struct {
	systemPrompt string
	completion   completion
}

func NewAgent(client *Client, prompt string) *Agent {
	return &Agent{completion: client, systemPrompt: strings.TrimSpace(prompt)}
}

func newAgent(completion completion, prompt string) *Agent {
	return &Agent{completion: completion, systemPrompt: strings.TrimSpace(prompt)}
}

func (a *Agent) Ask(ctx context.Context, question, contextText string) (*domain.AnalysisResponse, error) {
	question = strings.TrimSpace(question)
	if question == "" {
		return nil, errors.New("question is empty")
	}
	if len([]rune(question)) > 4000 {
		return nil, errors.New("question exceeds 4000 characters")
	}

	userContent := fmt.Sprintf("SYSTEM CONTEXT:"+"\n"+"%s"+"\n\n"+"USER QUESTION:"+"\n"+"%s", contextText, question)
	content, err := a.completion.Complete(ctx, a.systemPrompt, userContent)
	if err != nil {
		return nil, err
	}
	var result domain.AnalysisResponse
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidResponse, err)
	}
	if err := validateAnalysisResponse(result, len([]byte(content))); err != nil {
		return nil, err
	}
	return &result, nil
}
