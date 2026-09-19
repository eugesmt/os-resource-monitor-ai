package ai

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

type fakeCompletion struct {
	systemPrompt string
	userContent  string
	calls        int
	answer       string
	err          error
	result       CompletionResult
}

func (f *fakeCompletion) Complete(_ context.Context, systemPrompt, userContent string) (CompletionResult, error) {
	f.calls++
	f.systemPrompt = systemPrompt
	f.userContent = userContent
	result := f.result
	if result.Content == "" {
		result.Content = f.answer
	}
	return result, f.err
}

func TestAgentAskValidatesAndComposesPrompt(t *testing.T) {
	fake := &fakeCompletion{answer: `{"diagnosis_schema_version":"diagnosis.v1","status":"healthy","summary":"answer","observations":[],"hypotheses":[],"missing_data":[],"recommendations":[]}`}
	answer, err := newAgent(fake, "test system prompt").Ask(context.Background(), "  what is happening?  ", "runtime context: unique")
	if err != nil {
		t.Fatalf("Ask() error = %v", err)
	}
	if answer == nil || answer.Summary != "answer" {
		t.Fatalf("answer = %#v, want parsed analysis", answer)
	}
	if fake.calls != 1 {
		t.Fatalf("transport calls = %d, want 1", fake.calls)
	}
	if fake.systemPrompt != "test system prompt" {
		t.Fatalf("system prompt = %q", fake.systemPrompt)
	}
	if !strings.Contains(fake.userContent, "USER QUESTION:") ||
		!strings.Contains(fake.userContent, "runtime context: unique") ||
		!strings.Contains(fake.userContent, "what is happening?") {
		t.Fatalf("user content has wrong composition: %q", fake.userContent)
	}
	if strings.Contains(fake.userContent, "contextTextMock") {
		t.Fatal("mock context leaked")
	}
}

func TestAgentAskValidationDoesNotCallTransport(t *testing.T) {
	for name, question := range map[string]string{"empty": " \t\n", "too long": strings.Repeat("я", 4001)} {
		t.Run(name, func(t *testing.T) {
			fake := &fakeCompletion{}
			_, err := newAgent(fake, "test system prompt").Ask(context.Background(), question, "context")
			if err == nil {
				t.Fatal("Ask() error = nil")
			}
			if fake.calls != 0 {
				t.Fatalf("transport calls = %d, want 0", fake.calls)
			}
		})
	}
}

func TestAgentAskReturnsCompletionError(t *testing.T) {
	wantErr := errors.New("upstream")
	_, err := newAgent(&fakeCompletion{err: wantErr}, "test system prompt").Ask(context.Background(), "question", "context")
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
}

func TestAgentAskRejectsInvalidStructuredResponse(t *testing.T) {
	cases := map[string]string{
		"missing summary":          `{"diagnosis_schema_version":"diagnosis.v1","status":"healthy","observations":[],"hypotheses":[],"missing_data":[],"recommendations":[]}`,
		"null observations":        `{"diagnosis_schema_version":"diagnosis.v1","status":"healthy","summary":"ok","observations":null,"hypotheses":[],"missing_data":[],"recommendations":[]}`,
		"empty metric":             `{"diagnosis_schema_version":"diagnosis.v1","status":"warning","summary":"ok","observations":[{"metric":"","value":1,"unit":"%","interpretation":"bad"}],"hypotheses":[],"missing_data":[],"recommendations":[]}`,
		"too many recommendations": `{"diagnosis_schema_version":"diagnosis.v1","status":"healthy","summary":"ok","observations":[],"hypotheses":[],"missing_data":[],"recommendations":["1","2","3","4","5","6"]}`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := newAgent(&fakeCompletion{answer: content}, "test system prompt").Ask(context.Background(), "question", "context")
			if !errors.Is(err, ErrInvalidResponse) {
				t.Fatalf("error = %v, want ErrInvalidResponse", err)
			}
		})
	}
}

func TestAgentAskRejectsUnknownFieldsAndTrailingJSON(t *testing.T) {
	valid := `{"diagnosis_schema_version":"diagnosis.v1","status":"healthy","summary":"ok","observations":[],"hypotheses":[],"missing_data":[],"recommendations":[]}`
	for name, content := range map[string]string{
		"unknown field":   strings.TrimSuffix(valid, "}") + `,"extra":"nope"}`,
		"trailing object": valid + ` {"another":true}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := newAgent(&fakeCompletion{answer: content}, "test system prompt").Ask(context.Background(), "question", "context")
			if !errors.Is(err, ErrInvalidResponse) || !errors.Is(err, ErrResponseParse) {
				t.Fatalf("error = %v, want strict parse failure", err)
			}
		})
	}
}

func TestAgentTelemetryKeepsOutcomeAndUnavailableUsage(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	completion := &fakeCompletion{
		result: CompletionResult{Content: `{"diagnosis_schema_version":"diagnosis.v1","status":"healthy","summary":"ok","observations":[],"hypotheses":[],"missing_data":[],"recommendations":[]}`, Model: "test-model", ConfigVersion: "test-config", ResponseID: "resp-1", FinishReason: "stop", Latency: 12},
	}
	agent := newAgent(completion, "test system prompt")
	agent.SetLogger(logger)
	if _, err := agent.Ask(context.Background(), "secret question", "secret snapshot"); err != nil {
		t.Fatalf("Ask() error = %v", err)
	}
	line := logs.String()
	for _, want := range []string{"outcome=success", "model=test-model", "config_version=test-config", "prompt_tokens=unavailable", "usage_available=false"} {
		if !strings.Contains(line, want) {
			t.Fatalf("telemetry missing %q: %s", want, line)
		}
	}
	if strings.Contains(line, "secret question") || strings.Contains(line, "secret snapshot") {
		t.Fatalf("sensitive content leaked in telemetry: %s", line)
	}
}
