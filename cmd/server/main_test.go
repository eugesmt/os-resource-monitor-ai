package main

import "testing"

func TestLoadAIMetricsConfigDefaults(t *testing.T) {
	for _, key := range []string{"AI_CONTEXT_WINDOW_TOKENS", "AI_INPUT_PRICE_PER_MILLION_TOKENS", "AI_OUTPUT_PRICE_PER_MILLION_TOKENS"} {
		t.Setenv(key, "")
	}
	config, err := loadAIMetricsConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.ContextWindowTokens != 0 || config.PricingConfigured {
		t.Fatalf("config = %#v", config)
	}
}

func TestLoadAIMetricsConfigParsesValues(t *testing.T) {
	t.Setenv("AI_CONTEXT_WINDOW_TOKENS", "128000")
	t.Setenv("AI_INPUT_PRICE_PER_MILLION_TOKENS", "0.14")
	t.Setenv("AI_OUTPUT_PRICE_PER_MILLION_TOKENS", "0.28")
	config, err := loadAIMetricsConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.ContextWindowTokens != 128000 || !config.PricingConfigured || config.InputPricePerMillionTokens != 0.14 || config.OutputPricePerMillionTokens != 0.28 {
		t.Fatalf("config = %#v", config)
	}
}

func TestLoadAIMetricsConfigRejectsInvalidValues(t *testing.T) {
	cases := []struct {
		name  string
		key   string
		value string
	}{
		{name: "context malformed", key: "AI_CONTEXT_WINDOW_TOKENS", value: "many"},
		{name: "context negative", key: "AI_CONTEXT_WINDOW_TOKENS", value: "-1"},
		{name: "price malformed", key: "AI_INPUT_PRICE_PER_MILLION_TOKENS", value: "usd"},
		{name: "price negative", key: "AI_OUTPUT_PRICE_PER_MILLION_TOKENS", value: "-0.1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, key := range []string{"AI_CONTEXT_WINDOW_TOKENS", "AI_INPUT_PRICE_PER_MILLION_TOKENS", "AI_OUTPUT_PRICE_PER_MILLION_TOKENS"} {
				t.Setenv(key, "")
			}
			t.Setenv(tc.key, tc.value)
			if _, err := loadAIMetricsConfig(); err == nil {
				t.Fatal("loadAIMetricsConfig() error = nil")
			}
		})
	}
}
