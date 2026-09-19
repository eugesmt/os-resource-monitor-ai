package domain

type DiagnosisResultV1 struct {
	SchemaVersion   string        `json:"diagnosis_schema_version"`
	Status          string        `json:"status"`
	Summary         string        `json:"summary"`
	Observations    []Observation `json:"observations"`
	Hypotheses      []string      `json:"hypotheses"`
	MissingData     []string      `json:"missing_data"`
	Recommendations []string      `json:"recommendations"`
}

// AnalysisResponse сохраняет публичное имя приложения, а wire-контракт использует V1.
type AnalysisResponse = DiagnosisResultV1

type Observation struct {
	Metric         string  `json:"metric"`
	Value          float64 `json:"value"`
	Unit           string  `json:"unit"`
	Interpretation string  `json:"interpretation"`
	PID            *int32  `json:"pid,omitempty"`
}

// Fact сохранён как совместимое имя для старых потребителей.
type Fact = Observation
