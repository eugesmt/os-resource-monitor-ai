package domain

type AnalysisResponse struct {
	Summary         string   `json:"summary"`
	Facts           []Fact   `json:"facts"`
	Recommendations []string `json:"recommendations"`
}

type Fact struct {
	Metric string  `json:"metric"`
	Value  float64 `json:"value"`
	Unit   string  `json:"unit"`
}
