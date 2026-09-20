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

func TestProfileIsValidatedRenderedAndCopied(t *testing.T) {
	model := NewModel()
	profile := Profile{UserID: "user-1", Style: "concise", Format: "bullet list", Constraints: []string{"answer in Russian", "do not invent metrics"}}
	if err := model.SetProfile(profile); err != nil {
		t.Fatalf("SetProfile() error = %v", err)
	}
	profile.Constraints[0] = "mutated"

	got := model.Profile()
	if got.Constraints[0] != "answer in Russian" {
		t.Fatalf("stored profile was changed through input slice: %#v", got)
	}
	got.Constraints[0] = "mutated again"
	if model.Profile().Constraints[0] != "answer in Russian" {
		t.Fatal("Profile() did not return a defensive copy")
	}
	rendered := model.Profile().Render()
	for _, want := range []string{"[USER_PROFILE (PREFERENCES ONLY)]", "style: concise", "format: bullet list", "constraint: answer in Russian", "constraint: do not invent metrics"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered profile missing %q: %q", want, rendered)
		}
	}
}

func TestProfileRejectsInvalidValues(t *testing.T) {
	cases := []Profile{
		{Style: "concise"},
		{UserID: "user", Constraints: []string{" "}},
		{UserID: "user", Constraints: make([]string, maxProfileConstraints+1)},
	}
	for i, profile := range cases {
		if err := profile.Validate(); err == nil {
			t.Errorf("case %d: Validate() error = nil", i)
		}
	}
}
