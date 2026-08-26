package watcher

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/RishabhKodes/agent-deck/internal/session"
)

// Clock abstracts time for rate-limiter and reaper determinism (D-26).
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
	NewTicker(d time.Duration) *time.Ticker
}

// realClock is the default production implementation of Clock.
type realClock struct{}

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }
func (realClock) NewTicker(d time.Duration) *time.Ticker { return time.NewTicker(d) }

// Triage rate-limiting and queue constants (INTEL-03, D-10/10a/10b).
const (
	TriageMaxPerHour = 5
	TriageWindow     = 60 * time.Minute
	TriageQueueCap   = 16
	TriageReqChCap   = 16
	TriageReaperPoll = 5 * time.Second
	TriageTimeout    = 10 * time.Minute
)

// TriageRequest is the work item the writerLoop hands to triageLoop.
type TriageRequest struct {
	Event      Event
	WatcherID  string
	Profile    string
	Tracker    *HealthTracker
	ResultPath string // absolute path: <TriageDir>/<DedupKey()>/result.json
	TriageDir  string // absolute: <EngineConfig.TriageDir>/<DedupKey()>/
	SpawnedAt  time.Time
}

// TriageSpawner launches a triage session for an unrouted event (D-25).
type TriageSpawner interface {
	Spawn(ctx context.Context, req TriageRequest) (sessionID string, err error)
}

// AgentDeckLaunchSpawner invokes `agent-deck launch` to spawn a triage Claude session.
// BinaryPath is resolved via session.FindAgentDeck() at engine startup (D-01/D-03).
type AgentDeckLaunchSpawner struct {
	BinaryPath string
}

// Spawn creates the triage directory, builds the prompt, and exec's agent-deck launch.
// Returns an error without spawning if the binary does not exist (T-18-13).
func (s AgentDeckLaunchSpawner) Spawn(ctx context.Context, req TriageRequest) (string, error) {
	bin := s.BinaryPath
	if bin == "" {
		bin = session.FindAgentDeck()
	}
	if bin == "" {
		return "", fmt.Errorf("triage_spawner: agent-deck binary not found in PATH or standard locations")
	}

	// Verify the binary exists before attempting to exec (T-18-13).
	if _, err := os.Stat(bin); err != nil {
		return "", fmt.Errorf("triage_spawner: binary %q: %w", bin, err)
	}

	// Create triage output directory so the Claude session can write result.json.
	if err := os.MkdirAll(req.TriageDir, 0o700); err != nil {
		return "", fmt.Errorf("triage_spawner: mkdir %q: %w", req.TriageDir, err)
	}

	// Build the triage prompt.
	// ClientsPath prompt: we pass an empty clientsList here; the real engine wires clientsPath.
	// For the spawner the prompt is assembled by the engine before calling Spawn.
	// Use a short-hash for the session title (D-02).
	dedupKey := req.Event.DedupKey()
	shortHash := dedupKey
	if len(shortHash) > 8 {
		shortHash = shortHash[:8]
	}
	sessionTitle := "triage-" + shortHash

	// Build the prompt from the request.ResultPath (engine pre-builds the full prompt and
	// stores it; for now AgentDeckLaunchSpawner builds a minimal prompt).
	// The full prompt is built externally via BuildPrompt and embedded in TriageRequest.
	// To keep the interface simple, we accept the prompt as a serialized string via a
	// convention: if req.TriageDir has a "prompt.txt", use that; otherwise build minimal.
	// For the integration test, the spawner receives the full prompt pre-built by triageLoop.
	promptPath := filepath.Join(req.TriageDir, "prompt.txt")
	promptBytes, err := os.ReadFile(promptPath)
	var prompt string
	if err != nil {
		// Fallback: build a simple prompt inline.
		prompt = fmt.Sprintf(
			"You are a routing classifier. Write routing decision as JSON to: %s\n"+
				"JSON fields: route_to, group, name, sender, summary, confidence (high/medium/low), should_persist (bool)\n"+
				"Event sender: %s Subject: %s",
			req.ResultPath, req.Event.Sender, req.Event.Subject,
		)
	} else {
		prompt = string(promptBytes)
	}

	args := []string{
		"launch",
		"--no-parent",
		"-c", "claude",
		"-g", "triage",
		"-t", sessionTitle,
		"-m", prompt,
	}
	if req.Profile != "" {
		args = append([]string{"-p", req.Profile}, args...)
	}

	cmd := exec.CommandContext(ctx, bin, args...)
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("triage_spawner: launch: %w", err)
	}

	// Reap the child asynchronously so its exit doesn't become a zombie.
	// Prior to v1.7.43 this was fire-and-forget and produced one zombie
	// per triage spawn (#677).
	go func() { _ = cmd.Wait() }()

	return sessionTitle, nil
}

// rateLimiter is an in-memory rolling-window rate limiter (D-10/10a/10b).
// Not safe for concurrent use without external synchronization — triageLoop
// is the only goroutine that accesses it.
type rateLimiter struct {
	spawns []time.Time
}

// tryAcquire returns true if a new spawn is allowed, pruning stale entries first.
// If the rolling window is full, returns false without mutating the slice.
func (r *rateLimiter) tryAcquire(now time.Time) bool {
	cutoff := now.Add(-TriageWindow)
	// Prune entries outside the rolling window.
	pruned := r.spawns[:0]
	for _, ts := range r.spawns {
		if ts.After(cutoff) {
			pruned = append(pruned, ts)
		}
	}
	r.spawns = pruned

	if len(r.spawns) >= TriageMaxPerHour {
		return false
	}
	r.spawns = append(r.spawns, now)
	return true
}
