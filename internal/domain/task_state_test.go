package domain

import "testing"

func TestTaskStatePauseResumePreservesCheckpoint(t *testing.T) {
	state := NewTaskState("task-1")
	var err error
	state, err = state.Apply(TaskActionUpdate, TaskPhasePlanning, "collect requirements", "ask for constraints")
	if err != nil {
		t.Fatal(err)
	}
	paused, err := state.Apply(TaskActionPause, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if paused.Status != TaskStatusPaused || paused.Step != state.Step || paused.ExpectedAction != state.ExpectedAction {
		t.Fatalf("pause lost checkpoint: %#v", paused)
	}
	resumed, err := paused.Apply(TaskActionResume, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Status != TaskStatusActive || resumed.Step != state.Step || resumed.ExpectedAction != state.ExpectedAction {
		t.Fatalf("resume lost checkpoint: %#v", resumed)
	}
}

func TestTaskStateRequiresOrderedPhaseTransitions(t *testing.T) {
	state := NewTaskState("task-1")
	if _, err := state.Apply(TaskActionAdvance, TaskPhaseValidation, "", ""); err == nil {
		t.Fatal("skipping execution phase was accepted")
	}
	state, err := state.Apply(TaskActionAdvance, TaskPhaseExecution, "run checks", "wait for result")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.Apply(TaskActionUpdate, TaskPhasePlanning, "", ""); err == nil {
		t.Fatal("backward phase update was accepted")
	}
}

func TestTaskDecisionCompletesOnlyAfterValidation(t *testing.T) {
	state := NewTaskState("task-1")
	var err error
	state, err = state.Apply(TaskActionAdvance, TaskPhaseExecution, "execute", "validate result")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.ApplyDecision(TaskDecision{Action: TaskDecisionComplete}); err == nil {
		t.Fatal("execution task completed without validation")
	}
	state, err = state.Apply(TaskActionAdvance, TaskPhaseValidation, "validate", "confirm result")
	if err != nil {
		t.Fatal(err)
	}
	done, err := state.ApplyDecision(TaskDecision{Action: TaskDecisionComplete})
	if err != nil {
		t.Fatal(err)
	}
	if done.Phase != TaskPhaseDone || done.ExpectedActionKind != TaskActionKindNone || done.ExpectedAction != "" {
		t.Fatalf("completed state = %#v", done)
	}
}

func TestTaskDecisionValidationMayReturnToExecution(t *testing.T) {
	state := NewTaskState("task-1")
	var err error
	state, err = state.ApplyDecision(TaskDecision{Action: TaskDecisionAdvance, Phase: TaskPhaseExecution, Step: "execute", ExpectedActionKind: TaskActionKindAgent, ExpectedAction: "run checks"})
	if err != nil {
		t.Fatal(err)
	}
	state, err = state.ApplyDecision(TaskDecision{Action: TaskDecisionAdvance, Phase: TaskPhaseValidation, Step: "validate", ExpectedActionKind: TaskActionKindAgent, ExpectedAction: "check result"})
	if err != nil {
		t.Fatal(err)
	}
	retry, err := state.ApplyDecision(TaskDecision{Action: TaskDecisionAdvance, Phase: TaskPhaseExecution, Step: "fix", ExpectedActionKind: TaskActionKindTool, ExpectedAction: "run tool again"})
	if err != nil {
		t.Fatal(err)
	}
	if retry.Phase != TaskPhaseExecution || retry.ExpectedActionKind != TaskActionKindTool {
		t.Fatalf("retry state = %#v", retry)
	}
}

func TestTaskCancelIsTerminalAndPreservesNoCheckpoint(t *testing.T) {
	state := NewTaskState("task-1")
	state, err := state.ApplyDecision(TaskDecision{Action: TaskDecisionStay, Phase: TaskPhasePlanning, Step: "plan", ExpectedActionKind: TaskActionKindAgent, ExpectedAction: "run next step"})
	if err != nil {
		t.Fatal(err)
	}
	cancelled, err := state.Apply(TaskActionCancel, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Phase != TaskPhaseDone || cancelled.Outcome != TaskOutcomeCancelled || cancelled.Step != "" || cancelled.ExpectedAction != "" || cancelled.ExpectedActionKind != TaskActionKindNone {
		t.Fatalf("cancelled state = %#v", cancelled)
	}
	if _, err := cancelled.Apply(TaskActionResume, "", "", ""); err == nil {
		t.Fatal("cancelled task was resumed")
	}
}
