package memory

import (
	"strings"
	"testing"
)

func TestMemoryRecordsStayInExplicitLayers(t *testing.T) {
	model := NewModel()
	short, err := model.RememberShortTerm("conversation-1", "user_message", "GPU is warm")
	if err != nil {
		t.Fatal(err)
	}
	working, err := model.SetWorking("task-1", "decision", "Compare GPU temperature with yesterday")
	if err != nil {
		t.Fatal(err)
	}
	long, err := model.RememberLongTerm("user-1", "preference", "Answer in Russian")
	if err != nil {
		t.Fatal(err)
	}

	if short.Layer != ShortTerm || working.Layer != Working || long.Layer != LongTerm {
		t.Fatalf("layers = %#v %#v %#v", short.Layer, working.Layer, long.Layer)
	}
	context := model.BuildContext(MemorySelection{ConversationID: "conversation-1", TaskID: "task-1", LongTermScope: "user-1", IncludeShort: true, IncludeWorking: true, IncludeLong: true})
	if len(context.ShortTerm) != 1 || context.ShortTerm[0].ID != short.ID {
		t.Fatalf("short-term = %#v", context.ShortTerm)
	}
	if len(context.Working) != 1 || context.Working[0].ID != working.ID {
		t.Fatalf("working = %#v", context.Working)
	}
	if len(context.LongTerm) != 1 || context.LongTerm[0].ID != long.ID {
		t.Fatalf("long-term = %#v", context.LongTerm)
	}
	rendered := context.Render()
	for _, section := range []string{"[MEMORY short_term]", "[MEMORY working]", "[MEMORY long_term]", "GPU is warm", "Compare GPU", "Answer in Russian"} {
		if !strings.Contains(rendered, section) {
			t.Fatalf("rendered context missing %q: %s", section, rendered)
		}
	}
}

func TestMemoryScopesAndExplicitSelection(t *testing.T) {
	model := NewModel()
	if _, err := model.RememberShortTerm("conversation-1", "message", "only one conversation"); err != nil {
		t.Fatal(err)
	}
	if _, err := model.SetWorking("task-1", "constraint", "only one task"); err != nil {
		t.Fatal(err)
	}
	if _, err := model.RememberLongTerm("user-1", "knowledge", "durable fact"); err != nil {
		t.Fatal(err)
	}

	context := model.BuildContext(MemorySelection{ConversationID: "conversation-2", TaskID: "task-2", LongTermScope: "user-2", IncludeShort: true, IncludeWorking: true, IncludeLong: true})
	if !context.Empty() {
		t.Fatalf("unrelated scopes leaked into context: %#v", context)
	}

	context = model.BuildContext(MemorySelection{ConversationID: "conversation-1", IncludeShort: true})
	if len(context.ShortTerm) != 1 || len(context.Working) != 0 || len(context.LongTerm) != 0 {
		t.Fatalf("explicit selection = %#v", context)
	}
}

func TestMemoryDoesNotAutoPromoteShortTermToLongTerm(t *testing.T) {
	model := NewModel()
	if _, err := model.RememberShortTerm("conversation-1", "user_message", "my machine has a GPU"); err != nil {
		t.Fatal(err)
	}
	context := model.BuildContext(MemorySelection{LongTermScope: "user-1", IncludeLong: true})
	if !context.Empty() {
		t.Fatalf("short-term record was auto-promoted: %#v", context)
	}
}

func TestMemoryRejectsEmptyFields(t *testing.T) {
	model := NewModel()
	if _, err := model.RememberShortTerm("", "message", "content"); err == nil {
		t.Fatal("empty conversation scope accepted")
	}
	if _, err := model.SetWorking("task", "", "content"); err == nil {
		t.Fatal("empty kind accepted")
	}
	if _, err := model.RememberLongTerm("user", "fact", ""); err == nil {
		t.Fatal("empty content accepted")
	}
}
