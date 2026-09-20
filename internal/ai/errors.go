package ai

import (
	"errors"
	"time"
)

const RequestTimeout = 45 * time.Second

var (
	ErrUpstream                = errors.New("AI upstream failure")
	ErrTimeout                 = errors.New("AI request timeout")
	ErrTruncated               = errors.New("AI response truncated")
	ErrInvalidResponse         = errors.New("invalid AI response")
	ErrResponseParse           = errors.New("AI response parse failure")
	ErrResponseStructure       = errors.New("AI response structural validation failure")
	ErrPromptNotConfigured     = errors.New("AI system prompt is not configured")
	ErrContentFiltered         = errors.New("AI response content filtered")
	ErrToolCalls               = errors.New("AI returned tool calls")
	ErrAbnormalFinish          = errors.New("AI completion finished abnormally")
	ErrSemanticValidation      = errors.New("AI response semantic validation failure")
	ErrConversationPersistence = errors.New("conversation persistence failure")
	ErrSnapshotUnavailable     = errors.New("snapshot is required for AI analysis")
	ErrRequiredContextTooLarge = errors.New("required AI context exceeds token budget")
	ErrHistoryUnavailable      = errors.New("conversation history unavailable")
)
