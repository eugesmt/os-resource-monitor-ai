package ai

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

const systemPrompt = `Ты AI-ассистент системного диспетчера задач.
Анализируй только предоставленные системные метрики.
Не выдумывай значения, которых нет в контексте.
Отделяй факты от предположений.
Давай краткие, практичные и понятные рекомендации.`

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

func (c *Client) Ask(ctx context.Context, question, contextText string) (string, error) {
	question = strings.TrimSpace(question)
	if question == "" {
		return "", errors.New("question is empty")
	}
	if len([]rune(question)) > 4000 {
		return "", errors.New("question exceeds 4000 characters")
	}

	select {
	case c.sem <- struct{}{}:
		defer func() { <-c.sem }()
	case <-ctx.Done():
		return "", ctx.Err()
	}

	requestCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	userContent := fmt.Sprintf("SYSTEM CONTEXT:\n%s\n\nUSER QUESTION:\n%s", contextText, question)
	resp, err := c.client.CreateChatCompletion(requestCtx, openai.ChatCompletionRequest{
		Model: c.model,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: systemPrompt},
			{Role: openai.ChatMessageRoleUser, Content: userContent},
		},
	})
	if err != nil {
		if errors.Is(requestCtx.Err(), context.DeadlineExceeded) {
			return "", errors.New("AI request timed out after 30 seconds")
		}
		return "", fmt.Errorf("AI request failed: %w", err)
	}
	if len(resp.Choices) == 0 {
		return "", errors.New("AI returned an empty response")
	}
	return strings.TrimSpace(resp.Choices[0].Message.Content), nil
}
