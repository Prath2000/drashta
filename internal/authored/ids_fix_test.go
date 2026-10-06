package authored

import "testing"

// M14: Validate checks like Save does, before any id exists, and never changes the item.
func TestValidateDoesNotChangeItem(t *testing.T) {
	task := &Task{Title: "ok"}
	if err := Validate(task); err != nil {
		t.Fatal(err)
	}
	if task.ID != "" || task.Status != "" || task.Links != nil {
		t.Fatalf("Validate changed the item: %+v", task)
	}
	for _, bad := range []any{
		&Task{Title: "two\nlines"},
		&Task{Title: "x", Status: "weird"},
		&Task{Title: "x", Body: "a\n## Log\nb"},
		&Feature{Title: ""},
		&Milestone{Title: "m", State: "half"},
		Note{Ref: "task:T-1", Body: "x", X: new(float64)}, // a pin needs both x and y
	} {
		if err := Validate(bad); err == nil {
			t.Errorf("Validate(%+v) = nil, want an error", bad)
		}
	}
	if err := Validate(42); err == nil {
		t.Error("Validate(42) = nil")
	}
}
