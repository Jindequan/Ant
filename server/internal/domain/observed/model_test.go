package observed

import "testing"

func TestModelIDNormalizesOnlyCaseAndVendorPrefix(t *testing.T) {
	for in, want := range map[string]string{"GLM-5.3": "glm-5.3", "zhipu/glm-5.3": "glm-5.3", " deepseek-v4 ": "deepseek-v4", "openrouter/anthropic/claude-x": "claude-x", "gpt-5.5-2026-01-01": "gpt-5.5-2026-01-01"} {
		if got := ModelID(in); got != want {
			t.Errorf("ModelID(%q) = %q, want %q", in, got, want)
		}
	}
}
