package domain

import "time"

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
