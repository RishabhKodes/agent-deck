package feedback_test

import (
	"regexp"
	"testing"

	"github.com/RishabhKodes/agent-deck/internal/feedback"
	"github.com/stretchr/testify/require"
)

// TestSender_DiscussionNodeID_IsReal locks the expected shape of the GitHub
// Discussion node ID constant. It is RED against the placeholder
// ("D_PLACEHOLDER") shipped on this branch and turns GREEN once the real
// node ID (shape: D_kw...) is pasted in Phase 2.
//
// Two assertions by design:
//  1. Direct inequality vs. "D_PLACEHOLDER" — catches the exact current
//     regression and any future attempt to reintroduce the placeholder.
//  2. Regex ^D_[A-Za-z0-9_-]{10,}$ — catches typos, truncated IDs, or
//     anything that doesn't match the GraphQL node ID shape.
//
// Requirements: REQ-FB-2.
func TestSender_DiscussionNodeID_IsReal(t *testing.T) {
	require.NotEqual(t, "D_PLACEHOLDER", feedback.DiscussionNodeID,
		"DiscussionNodeID must be replaced with the real GitHub Discussion node ID before release")

	re := regexp.MustCompile(`^D_[A-Za-z0-9_-]{10,}$`)
	require.Regexp(t, re, feedback.DiscussionNodeID,
		"DiscussionNodeID must match GitHub GraphQL node ID shape ^D_[A-Za-z0-9_-]{10,}$")
}
