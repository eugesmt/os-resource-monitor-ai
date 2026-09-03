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

const systemPrompt = `Ты AI-ассистент системного диспетчера задач.

Анализируй только предоставленные системные метрики.
Не выдумывай значения, которых нет в контексте.
Отделяй факты от предположений.
Давай краткие, практичные и понятные рекомендации.

Верни результат строго в JSON следующего формата:
{
  "summary": "string",
  "facts": [
    {
      "metric": "string",
      "value": 0,
      "unit": "string"
    }
  ],
  "recommendations": [
    "string"
  ]
}

Не добавляй markdown, пояснения или текст вне JSON.
После формирования полного JSON немедленно заверши ответ.`

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
			Type: openai.ChatCompletionResponseFormatTypeJSONObject,
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
