package invariants

import "testing"

func TestModelCopiesAndRendersActiveInvariants(t *testing.T) {
	model := NewModel()
	items := []Invariant{
		{ID: "disabled", Category: "stack", Statement: "ignored", Enabled: false},
		{ID: "architecture", Category: "architecture", Statement: "keep monolith", Enabled: true},
	}
	if err := model.Replace(items); err != nil {
		t.Fatal(err)
	}
	items[1].Statement = "mutated"
	active := model.Active()
	if len(active) != 1 || active[0].Statement != "keep monolith" {
		t.Fatalf("active = %#v", active)
	}
	if rendered := Render(active); rendered == "" || !containsAll(rendered, "PROJECT_INVARIANTS", "architecture", "keep monolith") {
		t.Fatalf("rendered = %q", rendered)
	}
}

func TestModelRejectsInvalidOrDuplicateIDs(t *testing.T) {
	model := NewModel()
	if err := model.Replace([]Invariant{{ID: "same", Category: "stack", Statement: "one"}, {ID: "same", Category: "stack", Statement: "two"}}); err == nil {
		t.Fatal("expected duplicate error")
	}
	if err := model.Replace([]Invariant{{ID: "", Category: "stack", Statement: "one"}}); err == nil {
		t.Fatal("expected validation error")
	}
}

func containsAll(value string, wants ...string) bool {
	for _, want := range wants {
		if !contains(value, want) {
			return false
		}
	}
	return true
}

func contains(value, want string) bool {
	for i := 0; i+len(want) <= len(value); i++ {
		if value[i:i+len(want)] == want {
			return true
		}
	}
	return false
}
