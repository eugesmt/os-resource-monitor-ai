package ai

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeCompletion struct {
	systemPrompt string
	userContent  string
	calls        int
	answer       string
	err          error
}

func (f *fakeCompletion) Complete(_ context.Context, systemPrompt, userContent string) (string, error) {
	f.calls++
	f.systemPrompt = systemPrompt
	f.userContent = userContent
	return f.answer, f.err
}

func TestAgentAskValidatesAndComposesPrompt(t *testing.T) {
	fake := &fakeCompletion{answer: `{"summary":"answer","facts":[],"recommendations":[]}`}
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
	if !strings.Contains(fake.userContent, "SYSTEM CONTEXT:") ||
		!strings.Contains(fake.userContent, "runtime context: unique") ||
		!strings.Contains(fake.userContent, "USER QUESTION:") ||
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
		"missing summary":          `{"facts":[],"recommendations":[]}`,
		"null facts":               `{"summary":"ok","facts":null,"recommendations":[]}`,
		"empty metric":             `{"summary":"ok","facts":[{"metric":"","value":1,"unit":"%"}],"recommendations":[]}`,
		"too many recommendations": `{"summary":"ok","facts":[],"recommendations":["1","2","3","4","5","6"]}`,
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
