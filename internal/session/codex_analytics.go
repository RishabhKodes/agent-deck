package session

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

const codexContextBaselineTokens int64 = 12_000

// CodexSessionAnalytics is the latest token-count snapshot from a Codex
// rollout. TotalTokens is lifetime usage; CurrentContextTokens comes from
// last_token_usage and is the value that reflects current context pressure.
type CodexSessionAnalytics struct {
	InputTokens          int64
	CachedInputTokens    int64
	OutputTokens         int64
	ReasoningTokens      int64
	TotalTokens          int64
	CurrentContextTokens int64
	ContextWindow        int64
}

// ContextPercent returns used context using the same 12k-token baseline that
// Codex's own TUI excludes from its context-window percentage.
func (a *CodexSessionAnalytics) ContextPercent() float64 {
	if a == nil || a.ContextWindow <= codexContextBaselineTokens {
		return 0
	}
	effectiveWindow := a.ContextWindow - codexContextBaselineTokens
	used := a.CurrentContextTokens - codexContextBaselineTokens
	if used < 0 {
		used = 0
	}
	percent := float64(used) / float64(effectiveWindow) * 100
	if percent < 0 {
		return 0
	}
	if percent > 100 {
		return 100
	}
	return percent
}

// GetCodexAnalytics resolves this instance's account-aware rollout and parses
// its latest token-count event.
func (i *Instance) GetCodexAnalytics() (*CodexSessionAnalytics, error) {
	if i == nil || !IsCodexCompatible(i.Tool) || i.CodexSessionID == "" || i.isRemoteSession() {
		return nil, nil
	}
	path := codexRolloutPathInHome(i.CodexSessionID, i.getCodexHomeDir())
	if path == "" {
		return nil, nil
	}
	return ParseCodexRolloutAnalytics(path)
}

// ParseCodexRolloutAnalytics reads the latest non-null token_count info record.
// Unknown event fields are ignored so this remains forward-compatible with the
// append-only rollout schema.
func ParseCodexRolloutAnalytics(path string) (*CodexSessionAnalytics, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	type tokenUsage struct {
		InputTokens       int64 `json:"input_tokens"`
		CachedInputTokens int64 `json:"cached_input_tokens"`
		OutputTokens      int64 `json:"output_tokens"`
		ReasoningTokens   int64 `json:"reasoning_output_tokens"`
		TotalTokens       int64 `json:"total_tokens"`
	}
	type tokenInfo struct {
		TotalTokenUsage    tokenUsage `json:"total_token_usage"`
		LastTokenUsage     tokenUsage `json:"last_token_usage"`
		ModelContextWindow int64      `json:"model_context_window"`
	}
	var latest *CodexSessionAnalytics

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 256*1024), 16*1024*1024)
	for scanner.Scan() {
		var entry struct {
			Type    string `json:"type"`
			Payload struct {
				Type string     `json:"type"`
				Info *tokenInfo `json:"info"`
			} `json:"payload"`
		}
		if json.Unmarshal(scanner.Bytes(), &entry) != nil || entry.Type != "event_msg" ||
			entry.Payload.Type != "token_count" || entry.Payload.Info == nil {
			continue
		}
		info := entry.Payload.Info
		latest = &CodexSessionAnalytics{
			InputTokens:          info.TotalTokenUsage.InputTokens,
			CachedInputTokens:    info.TotalTokenUsage.CachedInputTokens,
			OutputTokens:         info.TotalTokenUsage.OutputTokens,
			ReasoningTokens:      info.TotalTokenUsage.ReasoningTokens,
			TotalTokens:          info.TotalTokenUsage.TotalTokens,
			CurrentContextTokens: info.LastTokenUsage.TotalTokens,
			ContextWindow:        info.ModelContextWindow,
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read Codex rollout analytics: %w", err)
	}
	return latest, nil
}
