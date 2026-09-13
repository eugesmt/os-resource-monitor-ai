package ai

import (
	"errors"
	"time"
)

const RequestTimeout = 45 * time.Second

var (
	ErrUpstream              = errors.New("AI upstream failure")
	ErrTimeout               = errors.New("AI request timeout")
	ErrTruncated             = errors.New("AI response truncated")
	ErrInvalidResponse       = errors.New("invalid AI response")
	ErrPromptNotConfigured   = errors.New("AI system prompt is not configured")
	ErrContextWindowExceeded = errors.New("AI context window exceeded")
)
