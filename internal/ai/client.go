package ai

import (
	"context"
	"errors"
	"fmt"
	"strings"

	openai "github.com/sashabaranov/go-openai"
)

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

func (c *Client) Complete(ctx context.Context, systemPrompt, userContent string) (string, error) {
	select {
	case c.sem <- struct{}{}:
		defer func() { <-c.sem }()
	case <-ctx.Done():
		return "", ctx.Err()
	}

	requestCtx, cancel := context.WithTimeout(ctx, RequestTimeout)
	defer cancel()

	resp, err := c.client.CreateChatCompletion(requestCtx, openai.ChatCompletionRequest{
		Model: c.model,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: systemPrompt},
			{Role: openai.ChatMessageRoleUser, Content: userContent},
		},
		MaxTokens: 2000,
		ResponseFormat: &openai.ChatCompletionResponseFormat{
			Type: openai.ChatCompletionResponseFormatTypeJSONObject,
		},
	})
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return "", ctx.Err()
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(requestCtx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("%w: request timed out", ErrTimeout)
		}
		return "", fmt.Errorf("%w: AI request failed: %v", ErrUpstream, err)
	}
	if len(resp.Choices) == 0 {
		return "", errors.New("AI returned an empty response")
	}
	choice := resp.Choices[0]
	if choice.FinishReason == openai.FinishReasonLength {
		return "", ErrTruncated
	}
	content := strings.TrimSpace(choice.Message.Content)
	if content == "" {
		return "", errors.New("AI returned an empty response")
	}
	return content, nil
}
