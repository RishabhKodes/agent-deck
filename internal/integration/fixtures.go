package integration

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/RishabhKodes/agent-deck/internal/statedb"
	"github.com/stretchr/testify/require"
)

// NewTestDB creates an isolated SQLite database in a temp directory.
// Migrate is called automatically. The database is closed via t.Cleanup.
func NewTestDB(t *testing.T) *statedb.StateDB {
	t.Helper()

	tmpDir := t.TempDir()
	db, err := statedb.Open(filepath.Join(tmpDir, "state.db"))
	require.NoError(t, err, "statedb.Open should succeed")

	err = db.Migrate()
	require.NoError(t, err, "db.Migrate should succeed")

	t.Cleanup(func() { db.Close() })
	return db
}

// InstanceBuilder provides a fluent API for constructing statedb.InstanceRow values
// with sensible defaults for testing.
type InstanceBuilder struct {
	row *statedb.InstanceRow
}

// NewInstanceBuilder creates a builder with defaults:
// ProjectPath="/tmp/test", GroupPath="test-group", Tool="shell",
// Status="idle", CreatedAt=time.Now(), ToolData="{}".
func NewInstanceBuilder(id, title string) *InstanceBuilder {
	return &InstanceBuilder{
		row: &statedb.InstanceRow{
			ID:          id,
			Title:       title,
			ProjectPath: "/tmp/test",
			GroupPath:   "test-group",
			Tool:        "shell",
			Status:      "idle",
			CreatedAt:   time.Now(),
			ToolData:    json.RawMessage("{}"),
		},
	}
}

// WithTool sets the tool field.
func (b *InstanceBuilder) WithTool(tool string) *InstanceBuilder {
	b.row.Tool = tool
	return b
}

// WithStatus sets the status field.
func (b *InstanceBuilder) WithStatus(s string) *InstanceBuilder {
	b.row.Status = s
	return b
}

// WithParent sets the parent session ID.
func (b *InstanceBuilder) WithParent(id string) *InstanceBuilder {
	b.row.ParentSessionID = id
	return b
}

// WithProject sets the project path.
func (b *InstanceBuilder) WithProject(path string) *InstanceBuilder {
	b.row.ProjectPath = path
	return b
}

// WithGroup sets the group path.
func (b *InstanceBuilder) WithGroup(path string) *InstanceBuilder {
	b.row.GroupPath = path
	return b
}

// WithCommand sets the command field.
func (b *InstanceBuilder) WithCommand(cmd string) *InstanceBuilder {
	b.row.Command = cmd
	return b
}

// Build returns the constructed InstanceRow.
func (b *InstanceBuilder) Build() *statedb.InstanceRow {
	return b.row
}

// BuildSlice returns the constructed InstanceRow wrapped in a single-element slice.
// Convenience for db.SaveInstances().
func (b *InstanceBuilder) BuildSlice() []*statedb.InstanceRow {
	return []*statedb.InstanceRow{b.row}
}
