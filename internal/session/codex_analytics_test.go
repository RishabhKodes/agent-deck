package session

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseCodexRolloutAnalyticsUsesCurrentContextNotLifetimeTotal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	data := "{\"type\":\"event_msg\",\"payload\":{\"type\":\"token_count\",\"info\":null}}\n" +
		"{\"type\":\"event_msg\",\"payload\":{\"type\":\"token_count\",\"info\":{" +
		"\"total_token_usage\":{\"input_tokens\":490000,\"cached_input_tokens\":20000,\"output_tokens\":10000,\"reasoning_output_tokens\":5000,\"total_tokens\":500000}," +
		"\"last_token_usage\":{\"total_tokens\":62000},\"model_context_window\":212000}}}\n"
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := ParseCodexRolloutAnalytics(path)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.CurrentContextTokens != 62000 || got.TotalTokens != 500000 {
		t.Fatalf("analytics = %#v", got)
	}
	// Codex excludes its 12k baseline: (62k-12k)/(212k-12k) = 25%.
	if percent := got.ContextPercent(); percent != 25 {
		t.Fatalf("ContextPercent = %.2f, want 25", percent)
	}
}
