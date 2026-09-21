package domain

import (
	"fmt"
	"time"
)

type TaskPhase string

const (
	TaskPhasePlanning   TaskPhase = "planning"
	TaskPhaseExecution  TaskPhase = "execution"
	TaskPhaseValidation TaskPhase = "validation"
	TaskPhaseDone       TaskPhase = "done"
)

type TaskStatus string

const (
	TaskStatusActive TaskStatus = "active"
	TaskStatusPaused TaskStatus = "paused"
)

type TaskOutcome string

const (
	TaskOutcomeOpen      TaskOutcome = "open"
	TaskOutcomeCompleted TaskOutcome = "completed"
	TaskOutcomeCancelled TaskOutcome = "cancelled"
)

type TaskActionKind string

const (
	TaskActionKindAgent TaskActionKind = "agent"
	TaskActionKindUser  TaskActionKind = "user"
	TaskActionKindTool  TaskActionKind = "tool"
	TaskActionKindNone  TaskActionKind = "none"
)

type TaskDecisionKind string

const (
	TaskDecisionStay        TaskDecisionKind = "stay"
	TaskDecisionAdvance     TaskDecisionKind = "advance"
	TaskDecisionWaitForUser TaskDecisionKind = "wait_for_user"
	TaskDecisionComplete    TaskDecisionKind = "complete"
)

type TaskState struct {
	TaskID             string         `json:"task_id"`
	Goal               string         `json:"goal"`
	Phase              TaskPhase      `json:"phase"`
	Step               string         `json:"step"`
	ExpectedActionKind TaskActionKind `json:"expected_action_kind"`
	ExpectedAction     string         `json:"expected_action"`
	Status             TaskStatus     `json:"status"`
	Outcome            TaskOutcome    `json:"outcome"`
	LastError          string         `json:"last_error"`
	ResponseType       ResponseType   `json:"response_type,omitempty"`
	Version            int64          `json:"version"`
	UpdatedAt          time.Time      `json:"updated_at"`
}

type TaskTransition struct {
	ID             int64      `json:"id"`
	TaskID         string     `json:"task_id"`
	Action         string     `json:"action"`
	FromPhase      TaskPhase  `json:"from_phase"`
	ToPhase        TaskPhase  `json:"to_phase"`
	FromStatus     TaskStatus `json:"from_status"`
	ToStatus       TaskStatus `json:"to_status"`
	Step           string     `json:"step"`
	ExpectedAction string     `json:"expected_action"`
	CreatedAt      time.Time  `json:"created_at"`
}

type TaskDecision struct {
	Action             TaskDecisionKind `json:"action"`
	Phase              TaskPhase        `json:"phase,omitempty"`
	Step               string           `json:"current_step"`
	ExpectedActionKind TaskActionKind   `json:"expected_action_kind"`
	ExpectedAction     string           `json:"expected_action"`
}

type TaskStateAction string

const (
	TaskActionUpdate  TaskStateAction = "update"
	TaskActionAdvance TaskStateAction = "advance"
	TaskActionPause   TaskStateAction = "pause"
	TaskActionResume  TaskStateAction = "resume"
	TaskActionCancel  TaskStateAction = "cancel"
	TaskActionFail    TaskStateAction = "fail"
)

func NewTaskState(taskID string) TaskState {
	return TaskState{TaskID: taskID, Phase: TaskPhasePlanning, Status: TaskStatusActive, Outcome: TaskOutcomeOpen, ExpectedActionKind: TaskActionKindAgent}
}

func (s TaskState) Validate() error {
	if s.TaskID == "" {
		return fmt.Errorf("task id is required")
	}
	if !validTaskPhase(s.Phase) {
		return fmt.Errorf("invalid task phase %q", s.Phase)
	}
	if s.Status != TaskStatusActive && s.Status != TaskStatusPaused {
		return fmt.Errorf("invalid task status %q", s.Status)
	}
	if s.Outcome != TaskOutcomeOpen && s.Outcome != TaskOutcomeCompleted && s.Outcome != TaskOutcomeCancelled {
		return fmt.Errorf("invalid task outcome %q", s.Outcome)
	}
	if s.ResponseType != "" && s.ResponseType != ResponseTypeDiagnosis && s.ResponseType != ResponseTypeConversationRecall && s.ResponseType != ResponseTypeMetricComparison && s.ResponseType != ResponseTypeGeneralAnswer {
		return fmt.Errorf("invalid task response type %q", s.ResponseType)
	}
	if s.Phase == TaskPhaseDone && s.Status == TaskStatusPaused {
		return fmt.Errorf("done task cannot be paused")
	}
	if !validTaskActionKind(s.ExpectedActionKind) || s.ExpectedActionKind == "" {
		return fmt.Errorf("invalid expected action kind %q", s.ExpectedActionKind)
	}
	if s.Phase == TaskPhaseDone && (s.ExpectedActionKind != TaskActionKindNone || s.ExpectedAction != "") {
		return fmt.Errorf("done task must not have an expected action")
	}
	if s.Phase == TaskPhaseDone && s.Outcome == TaskOutcomeOpen {
		return fmt.Errorf("done task requires a terminal outcome")
	}
	if s.Phase != TaskPhaseDone && s.Outcome != TaskOutcomeOpen {
		return fmt.Errorf("non-terminal task must have open outcome")
	}
	if s.Phase != TaskPhaseDone && (s.Step == "") != (s.ExpectedAction == "") {
		return fmt.Errorf("task step and expected action must be set together")
	}
	return nil
}

func (s TaskState) Apply(action TaskStateAction, phase TaskPhase, step, expectedAction string) (TaskState, error) {
	if err := s.Validate(); err != nil {
		return TaskState{}, err
	}
	next := s
	switch action {
	case TaskActionPause:
		if s.Status == TaskStatusPaused || s.Phase == TaskPhaseDone {
			return TaskState{}, fmt.Errorf("task cannot be paused from %s/%s", s.Phase, s.Status)
		}
		next.Status = TaskStatusPaused
	case TaskActionResume:
		if s.Status != TaskStatusPaused {
			return TaskState{}, fmt.Errorf("task is not paused")
		}
		next.Status = TaskStatusActive
		next.LastError = ""
	case TaskActionFail:
		if s.Phase == TaskPhaseDone {
			return TaskState{}, fmt.Errorf("done task cannot fail")
		}
		next.Status = TaskStatusPaused
		next.LastError = expectedAction
	case TaskActionCancel:
		if s.Phase == TaskPhaseDone {
			return TaskState{}, fmt.Errorf("done task cannot be cancelled")
		}
		next.Phase, next.Status, next.Outcome = TaskPhaseDone, TaskStatusActive, TaskOutcomeCancelled
	case TaskActionUpdate:
		if s.Status == TaskStatusPaused {
			return TaskState{}, fmt.Errorf("paused task must be resumed before update")
		}
		if phase == "" {
			phase = s.Phase
		}
		if !validTaskPhase(phase) || phase != s.Phase {
			return TaskState{}, fmt.Errorf("update must keep current phase %q", s.Phase)
		}
		next.Step, next.ExpectedAction = step, expectedAction
	case TaskActionAdvance:
		if s.Status != TaskStatusActive {
			return TaskState{}, fmt.Errorf("paused task must be resumed before advance")
		}
		if phase != nextTaskPhase(s.Phase) {
			return TaskState{}, fmt.Errorf("invalid transition from %s to %s", s.Phase, phase)
		}
		next.Phase, next.Step, next.ExpectedAction = phase, step, expectedAction
	default:
		return TaskState{}, fmt.Errorf("invalid task action %q", action)
	}
	if next.Phase == TaskPhaseDone {
		next.Step, next.ExpectedAction, next.ExpectedActionKind = "", "", TaskActionKindNone
		next.LastError = ""
		if next.Outcome == TaskOutcomeOpen {
			next.Outcome = TaskOutcomeCompleted
		}
	}
	if err := next.Validate(); err != nil {
		return TaskState{}, err
	}
	return next, nil
}

func (s TaskState) ApplyDecision(decision TaskDecision) (TaskState, error) {
	if err := s.Validate(); err != nil {
		return TaskState{}, err
	}
	if !validTaskDecision(decision.Action) {
		return TaskState{}, fmt.Errorf("invalid task decision %q", decision.Action)
	}
	switch decision.Action {
	case TaskDecisionStay, TaskDecisionWaitForUser:
		if s.Phase == TaskPhaseDone || decision.Phase != s.Phase {
			return TaskState{}, fmt.Errorf("decision must keep current phase %q", s.Phase)
		}
		next := s
		next.Step, next.ExpectedAction, next.ExpectedActionKind = decision.Step, decision.ExpectedAction, decision.ExpectedActionKind
		if decision.Action == TaskDecisionWaitForUser {
			next.ExpectedActionKind = TaskActionKindUser
		}
		return next, next.Validate()
	case TaskDecisionAdvance:
		if !allowedTaskPhaseTransition(s.Phase, decision.Phase) || decision.Phase == TaskPhaseDone {
			return TaskState{}, fmt.Errorf("invalid transition from %s to %s", s.Phase, decision.Phase)
		}
		next := s
		next.Phase, next.Step, next.ExpectedActionKind, next.ExpectedAction = decision.Phase, decision.Step, decision.ExpectedActionKind, decision.ExpectedAction
		return next, next.Validate()
	case TaskDecisionComplete:
		if s.Phase != TaskPhaseValidation || (decision.Phase != "" && decision.Phase != TaskPhaseDone) {
			return TaskState{}, fmt.Errorf("task can complete only from validation")
		}
		next := s
		next.Phase, next.Step, next.ExpectedActionKind, next.ExpectedAction, next.Outcome = TaskPhaseDone, "", TaskActionKindNone, "", TaskOutcomeCompleted
		return next, next.Validate()
	default:
		return TaskState{}, fmt.Errorf("invalid task decision %q", decision.Action)
	}
}

func validTaskPhase(phase TaskPhase) bool {
	return phase == TaskPhasePlanning || phase == TaskPhaseExecution || phase == TaskPhaseValidation || phase == TaskPhaseDone
}

func validTaskActionKind(kind TaskActionKind) bool {
	return kind == "" || kind == TaskActionKindAgent || kind == TaskActionKindUser || kind == TaskActionKindTool || kind == TaskActionKindNone
}

func validTaskDecision(action TaskDecisionKind) bool {
	return action == TaskDecisionStay || action == TaskDecisionAdvance || action == TaskDecisionWaitForUser || action == TaskDecisionComplete
}

func allowedTaskPhaseTransition(from, to TaskPhase) bool {
	switch from {
	case TaskPhasePlanning:
		return to == TaskPhaseExecution
	case TaskPhaseExecution:
		return to == TaskPhaseValidation
	case TaskPhaseValidation:
		return to == TaskPhaseExecution
	default:
		return false
	}
}

func nextTaskPhase(phase TaskPhase) TaskPhase {
	switch phase {
	case TaskPhasePlanning:
		return TaskPhaseExecution
	case TaskPhaseExecution:
		return TaskPhaseValidation
	case TaskPhaseValidation:
		return TaskPhaseDone
	default:
		return ""
	}
}

type EventRole string

const (
	EventRoleUser      EventRole = "user"
	EventRoleAssistant EventRole = "assistant"
)

type ConversationEvent struct {
	ID             int64     `json:"id"`
	ConversationID string    `json:"conversation_id"`
	TurnID         string    `json:"turn_id"`
	Role           EventRole `json:"role"`
	Content        string    `json:"content"`
	SnapshotID     string    `json:"snapshot_id"`
	CreatedAt      time.Time `json:"created_at"`
}

type DiagnosisResultV1 struct {
	SchemaVersion   string        `json:"diagnosis_schema_version"`
	Status          string        `json:"status"`
	Summary         string        `json:"summary"`
	Observations    []Observation `json:"observations"`
	Hypotheses      []string      `json:"hypotheses"`
	MissingData     []string      `json:"missing_data"`
	Recommendations []string      `json:"recommendations"`
}

type ResponseType string

const (
	ResponseTypeDiagnosis          ResponseType = "diagnosis"
	ResponseTypeConversationRecall ResponseType = "conversation_recall"
	ResponseTypeMetricComparison   ResponseType = "metric_comparison"
	ResponseTypeGeneralAnswer      ResponseType = "general_answer"
)

const AssistantSchemaVersion = "assistant.v2"

type AssistantResponseV2 struct {
	SchemaVersion      string                      `json:"assistant_schema_version"`
	ResponseType       ResponseType                `json:"response_type"`
	TaskDecision       *TaskDecision               `json:"task_decision,omitempty"`
	Diagnosis          *DiagnosisResultV1          `json:"diagnosis,omitempty"`
	ConversationRecall *ConversationRecallResponse `json:"conversation_recall,omitempty"`
	MetricComparison   *MetricComparisonResponse   `json:"metric_comparison,omitempty"`
	GeneralAnswer      *GeneralAnswerResponse      `json:"general_answer,omitempty"`
	// Legacy convenience fields keep the public AnalysisResponse source-compatible
	// for diagnosis consumers while the envelope supports additional response modes.
	Status          string        `json:"-"`
	Summary         string        `json:"-"`
	Observations    []Observation `json:"-"`
	Hypotheses      []string      `json:"-"`
	MissingData     []string      `json:"-"`
	Recommendations []string      `json:"-"`
}

type ConversationRecallResponse struct {
	Answer   string  `json:"answer"`
	EventIDs []int64 `json:"event_ids"`
}

type MetricComparisonResponse struct {
	Answer string        `json:"answer"`
	Points []MetricPoint `json:"points"`
}

type GeneralAnswerResponse struct {
	Answer string `json:"answer"`
	Topic  string `json:"topic"`
}

type MetricPoint struct {
	Source     string  `json:"source"`
	Metric     string  `json:"metric"`
	Value      float64 `json:"value"`
	Unit       string  `json:"unit"`
	SnapshotID string  `json:"snapshot_id"`
	EventID    *int64  `json:"event_id,omitempty"`
	PID        *int32  `json:"pid,omitempty"`
}

// AnalysisResponse is the public API response envelope.
type AnalysisResponse = AssistantResponseV2

type Observation struct {
	Metric         string  `json:"metric"`
	Value          float64 `json:"value"`
	Unit           string  `json:"unit"`
	Interpretation string  `json:"interpretation"`
	PID            *int32  `json:"pid,omitempty"`
}

// Fact сохранён как совместимое имя для старых потребителей.
type Fact = Observation
