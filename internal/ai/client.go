package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"taskmanager-ai/internal/domain"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

type JSONSchema map[string]any

func (s JSONSchema) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any(s))
}

var schema = JSONSchema{
	"type": "object",
	"properties": map[string]any{
		"summary": map[string]any{
			"type": "string",
		},
		"facts": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"metric": map[string]any{
						"type": "string",
					},
					"value": map[string]any{
						"type": "number",
					},
					"unit": map[string]any{
						"type": "string",
					},
				},
				"required":             []string{"metric", "value"},
				"additionalProperties": false,
			},
		},
		"recommendations": map[string]any{
			"type":  "array",
			"items": map[string]any{"type": "string"},
		},
	},
	"required":             []string{"summary", "facts", "recommendations"},
	"additionalProperties": false,
}

const systemPrompt = `Ты AI-ассистент системного диспетчера задач.
Анализируй только предоставленные системные метрики.
Не выдумывай значения, которых нет в контексте.
Отделяй факты от предположений.
Давай краткие, практичные и понятные рекомендации.
После формирования полного ответа немедленно заверши генерацию.
Не добавляй никакого текста после завершения ответа.`

type Client struct {
	client *openai.Client
	model  string
	sem    chan struct{}
}

func NewClient(apiKey, baseURL, model string) *Client {
	config := openai.DefaultConfig(apiKey)
	if baseURL != "" {
		config.BaseURL = strings.TrimRight(baseURL, "/")
	}
	if model == "" {
		model = "deepseek-chat"
	}
	return &Client{
		client: openai.NewClientWithConfig(config),
		model:  model,
		sem:    make(chan struct{}, 1),
	}
}

func (c *Client) Ask(ctx context.Context, question, contextText string) (*domain.AnalysisResponse, error) {
	question = strings.TrimSpace(question)
	if question == "" {
		return nil, errors.New("question is empty")
	}
	if len([]rune(question)) > 4000 {
		return nil, errors.New("question exceeds 4000 characters")
	}

	select {
	case c.sem <- struct{}{}:
		defer func() { <-c.sem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	requestCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	userContent := fmt.Sprintf(
		"SYSTEM CONTEXT:\n%s\n\nUSER QUESTION:\n%s",
		contextText,
		question,
	)

	resp, err := c.client.CreateChatCompletion(requestCtx, openai.ChatCompletionRequest{
		Model: c.model,

		Messages: []openai.ChatCompletionMessage{
			{
				Role:    openai.ChatMessageRoleSystem,
				Content: systemPrompt,
			},
			{
				Role:    openai.ChatMessageRoleUser,
				Content: userContent,
			},
		},

		MaxCompletionTokens: 500,

		ResponseFormat: &openai.ChatCompletionResponseFormat{
			Type: openai.ChatCompletionResponseFormatTypeJSONSchema,
			JSONSchema: &openai.ChatCompletionResponseFormatJSONSchema{
				Name:        "system_metrics_analysis",
				Description: "Анализ системных метрик",
				Schema:      schema,
				Strict:      true,
			},
		},
	})

	if err != nil {
		if errors.Is(requestCtx.Err(), context.DeadlineExceeded) {
			return nil, errors.New("AI request timed out after 30 seconds")
		}
		return nil, fmt.Errorf("AI request failed: %w", err)
	}

	if len(resp.Choices) == 0 {
		return nil, errors.New("AI returned an empty response")
	}

	choice := resp.Choices[0]

	if choice.FinishReason == openai.FinishReasonLength {
		return nil, errors.New("AI response was truncated")
	}

	var result domain.AnalysisResponse

	if err := json.Unmarshal(
		[]byte(choice.Message.Content),
		&result,
	); err != nil {
		return nil, fmt.Errorf("invalid AI response: %w", err)
	}

	return &result, nil
}
