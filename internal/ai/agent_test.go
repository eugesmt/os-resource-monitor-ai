package ai

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"taskmanager-ai/internal/domain"
	"taskmanager-ai/internal/storage"
)

type fakeCompletion struct {
	systemPrompt string
	userContent  string
	messages     []domain.ChatMessage
	calls        int
	answer       string
	err          error
}

type fakeHistory struct {
	messages []domain.ChatMessage
	appended [][2]string
	err      error
}

func (f *fakeHistory) Load(context.Context) ([]domain.ChatMessage, error) {
	return append([]domain.ChatMessage(nil), f.messages...), f.err
}
func (f *fakeHistory) AppendTurn(_ context.Context, user, assistant string) error {
	f.appended = append(f.appended, [2]string{user, assistant})
	return f.err
}

func (f *fakeCompletion) Complete(_ context.Context, systemPrompt string, messages []domain.ChatMessage) (string, error) {
	f.calls++
	f.systemPrompt = systemPrompt
	f.messages = append([]domain.ChatMessage(nil), messages...)
	if len(messages) > 0 {
		f.userContent = messages[len(messages)-1].Content
	}
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
	if !strings.Contains(fake.userContent, "CURRENT REQUEST") ||
		!strings.Contains(fake.userContent, "SYSTEM CONTEXT (authoritative source for metrics):") ||
		!strings.Contains(fake.userContent, "runtime context: unique") ||
		!strings.Contains(fake.userContent, "USER QUESTION:") ||
		!strings.Contains(fake.userContent, "what is happening?") ||
		!strings.Contains(fake.userContent, "DIALOGUE HISTORY") {
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

func TestAgentAskUsesAndPersistsHistory(t *testing.T) {
	history := &fakeHistory{messages: []domain.ChatMessage{{Role: domain.ChatRoleUser, Content: "old question"}, {Role: domain.ChatRoleAssistant, Content: "old answer"}}}
	fake := &fakeCompletion{answer: `{"summary":"answer","facts":[],"recommendations":[]}`}
	if _, err := newAgent(fake, "system", history).Ask(context.Background(), "new question", "context"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fake.userContent, "new question") {
		t.Fatalf("current message = %q", fake.userContent)
	}
	if len(history.appended) != 1 || history.appended[0][0] != "new question" || history.appended[0][1] != fake.answer {
		t.Fatalf("appended = %#v", history.appended)
	}
}

func TestAgentHistorySurvivesReopen(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "chat.db")
	if output, err := exec.Command("goose", "-dir", filepath.Join("..", "..", "migrations"), "sqlite3", dbPath, "up").CombinedOutput(); err != nil {
		t.Fatalf("goose up: %v\n%s", err, output)
	}
	valid := `{"summary":"answer","facts":[],"recommendations":[]}`
	store, err := storage.OpenChatHistory(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	fake1 := &fakeCompletion{answer: valid}
	if _, err := newAgent(fake1, "system", store).Ask(context.Background(), "first", "context-1"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = storage.OpenChatHistory(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	fake2 := &fakeCompletion{answer: valid}
	if _, err := newAgent(fake2, "system", store).Ask(context.Background(), "second", "context-2"); err != nil {
		t.Fatal(err)
	}
	if len(fake2.messages) != 3 || fake2.messages[0].Content != "first" || fake2.messages[1].Content != valid || !strings.Contains(fake2.messages[2].Content, "context-2") {
		t.Fatalf("reopened messages = %#v", fake2.messages)
	}
	items, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 4 || items[0].Content != "first" || items[2].Content != "second" {
		t.Fatalf("persisted history = %#v", items)
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
