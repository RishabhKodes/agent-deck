package watcher

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/RishabhKodes/agent-deck/internal/statedb"
)

// triageResult is the structured output written by a triage Claude session (D-07).
type triageResult struct {
	RouteTo       string `json:"route_to"`
	Group         string `json:"group"`
	Name          string `json:"name"`
	Sender        string `json:"sender"`
	Summary       string `json:"summary"`
	Confidence    string `json:"confidence"` // "high" | "medium" | "low"
	ShouldPersist bool   `json:"should_persist"`
}

// birthEntry tracks the spawn metadata for a triage request (for timeout detection).
type birthEntry struct {
	watcherID string
	spawnedAt time.Time
}

// triageReaper polls the triage directory, processes result.json files, and updates
// the routing database. It handles high-confidence persistence, medium-confidence
// notifications, low-confidence logging, wildcard downgrading, timeouts, and malformed JSON.
type triageReaper struct {
	ctx          context.Context
	wg           *sync.WaitGroup
	clock        Clock
	triageDir    string
	clientsPath  string
	router       *Router
	db           *statedb.StateDB
	log          *slog.Logger
	pollInterval time.Duration

	// birth tracks spawn time + watcherID per dedupKey for timeout detection.
	birthMu sync.Mutex
	birth   map[string]birthEntry
}

// newTriageReaper constructs a triageReaper with default poll interval.
func newTriageReaper(
	ctx context.Context,
	wg *sync.WaitGroup,
	clock Clock,
	triageDir, clientsPath string,
	router *Router,
	db *statedb.StateDB,
	log *slog.Logger,
) *triageReaper {
	return &triageReaper{
		ctx:          ctx,
		wg:           wg,
		clock:        clock,
		triageDir:    triageDir,
		clientsPath:  clientsPath,
		router:       router,
		db:           db,
		log:          log,
		pollInterval: TriageReaperPoll,
		birth:        make(map[string]birthEntry),
	}
}

// registerBirth records that a triage session was spawned for the given dedupKey.
// Called by the engine after a successful Spawn.
func (r *triageReaper) registerBirth(dedupKey, watcherID string) {
	r.birthMu.Lock()
	defer r.birthMu.Unlock()
	r.birth[dedupKey] = birthEntry{
		watcherID: watcherID,
		spawnedAt: r.clock.Now(),
	}
}

// loop runs the reaper polling loop. Exits when ctx is cancelled.
func (r *triageReaper) loop() {
	defer r.wg.Done()

	ticker := r.clock.NewTicker(r.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			r.scanOnce()
		case <-r.ctx.Done():
			return
		}
	}
}

// scanOnce reads the triage directory and processes any pending results or timeouts.
func (r *triageReaper) scanOnce() {
	entries, err := os.ReadDir(r.triageDir)
	if err != nil {
		if !os.IsNotExist(err) {
			r.log.Debug("triage_reaper_readdir_failed",
				slog.String("dir", r.triageDir),
				slog.String("error", err.Error()),
			)
		}
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		hashDir := entry.Name()
		hashDirPath := filepath.Join(r.triageDir, hashDir)
		resultPath := filepath.Join(hashDirPath, "result.json")
		processedPath := filepath.Join(hashDirPath, "result.processed.json")

		// Skip already-processed results (idempotent).
		if _, err := os.Stat(processedPath); err == nil {
			continue
		}

		// Check for pending result.json.
		if _, err := os.Stat(resultPath); err == nil {
			r.processResult(hashDir, resultPath, processedPath)
			continue
		}

		// No result yet: check for timeout.
		r.birthMu.Lock()
		entry, ok := r.birth[hashDir]
		r.birthMu.Unlock()

		if ok {
			age := r.clock.Now().Sub(entry.spawnedAt)
			if age > TriageTimeout {
				r.handleTimeout(hashDir, entry.watcherID)
			}
		}
	}
}

// processResult parses and acts on a triage result.json file.
func (r *triageReaper) processResult(dedupKey, resultPath, processedPath string) {
	data, err := os.ReadFile(resultPath)
	if err != nil {
		r.log.Warn("triage_reaper_read_failed",
			slog.String("path", resultPath),
			slog.String("error", err.Error()),
		)
		r.markParseError(dedupKey, resultPath, processedPath, "read_error")
		return
	}

	var result triageResult
	if err := json.Unmarshal(data, &result); err != nil {
		r.log.Warn("triage_reaper_parse_failed",
			slog.String("path", resultPath),
			slog.String("error", err.Error()),
		)
		r.markParseError(dedupKey, resultPath, processedPath, "parse_error")
		return
	}

	// Validate confidence enum.
	switch result.Confidence {
	case "high", "medium", "low":
		// valid
	default:
		r.log.Warn("triage_reaper_invalid_confidence",
			slog.String("confidence", result.Confidence),
			slog.String("dedup_key", dedupKey),
		)
		r.markParseError(dedupKey, resultPath, processedPath, "invalid_confidence")
		return
	}

	// Wildcard detection: downgrade to medium (D-14, T-18-14).
	wildcardDowngraded := false
	if strings.HasPrefix(result.RouteTo, "*@") {
		r.log.Warn("triage_reaper_wildcard_downgraded",
			slog.String("route_to", result.RouteTo),
			slog.String("dedup_key", dedupKey),
		)
		result.Confidence = "medium"
		wildcardDowngraded = true
	}

	// Look up the watcher ID for this dedup key.
	watcherID, err := r.db.LookupWatcherIDByDedupKey(dedupKey)
	if err != nil {
		r.log.Warn("triage_reaper_lookup_failed",
			slog.String("dedup_key", dedupKey),
			slog.String("error", err.Error()),
		)
		// Still rename to processed to prevent infinite retries.
		_ = os.Rename(resultPath, processedPath)
		r.removeBirth(dedupKey)
		return
	}

	switch result.Confidence {
	case "high":
		if result.ShouldPersist {
			entry := ClientEntry{
				Conductor: result.RouteTo,
				Group:     result.Group,
				Name:      result.Name,
			}
			if appendErr := AppendClientEntry(r.clientsPath, result.Sender, entry); appendErr != nil {
				r.log.Warn("triage_reaper_append_failed",
					slog.String("sender", result.Sender),
					slog.String("error", appendErr.Error()),
				)
			} else {
				// Hot-reload the router with the updated clients.json (D-12).
				if newClients, loadErr := LoadClientsJSON(r.clientsPath); loadErr == nil {
					if r.router != nil {
						r.router.Reload(newClients)
					}
				}
			}
		}
		if dbErr := r.db.UpdateWatcherEventRoutedTo(watcherID, dedupKey, result.RouteTo, ""); dbErr != nil {
			r.log.Warn("triage_reaper_update_failed",
				slog.String("dedup_key", dedupKey),
				slog.String("error", dbErr.Error()),
			)
		}

	case "medium":
		marker := "triage-medium"
		if wildcardDowngraded {
			marker = "triage-medium-wildcard-downgraded"
		}
		// D-13: medium-confidence stub — conductor notification bridge not yet implemented.
		// TODO-D13-NOTIFY: medium-confidence triage for sender=%s, suggested route=%s
		r.log.Info("triage_medium_confidence",
			slog.String("TODO-D13-NOTIFY", "medium-confidence triage"),
			slog.String("sender", result.Sender),
			slog.String("suggested_route", result.RouteTo),
			slog.String("dedup_key", dedupKey),
		)
		if dbErr := r.db.UpdateWatcherEventRoutedTo(watcherID, dedupKey, marker, ""); dbErr != nil {
			r.log.Warn("triage_reaper_update_failed",
				slog.String("dedup_key", dedupKey),
				slog.String("error", dbErr.Error()),
			)
		}

	case "low":
		r.log.Info("triage_low_confidence",
			slog.String("sender", result.Sender),
			slog.String("dedup_key", dedupKey),
		)
		if dbErr := r.db.UpdateWatcherEventRoutedTo(watcherID, dedupKey, "triage-low-confidence", ""); dbErr != nil {
			r.log.Warn("triage_reaper_update_failed",
				slog.String("dedup_key", dedupKey),
				slog.String("error", dbErr.Error()),
			)
		}
	}

	// Rename result.json → result.processed.json (audit trail, idempotency guard).
	if err := os.Rename(resultPath, processedPath); err != nil {
		r.log.Warn("triage_reaper_rename_failed",
			slog.String("from", resultPath),
			slog.String("to", processedPath),
			slog.String("error", err.Error()),
		)
	}

	r.removeBirth(dedupKey)
}

// markParseError renames result.json to result.processed.json (preventing re-reads)
// and updates the DB with a parse-error marker.
func (r *triageReaper) markParseError(dedupKey, resultPath, processedPath, reason string) {
	watcherID, _ := r.db.LookupWatcherIDByDedupKey(dedupKey)
	if watcherID != "" {
		_ = r.db.UpdateWatcherEventRoutedTo(watcherID, dedupKey, "triage-parse-error", "")
	}
	if err := os.Rename(resultPath, processedPath); err != nil {
		r.log.Warn("triage_reaper_parse_error_rename_failed",
			slog.String("from", resultPath),
			slog.String("to", processedPath),
			slog.String("reason", reason),
			slog.String("error", err.Error()),
		)
	}
	r.removeBirth(dedupKey)
}

// handleTimeout marks the event with triage-timeout when no result arrives in time (D-09).
func (r *triageReaper) handleTimeout(dedupKey, watcherID string) {
	r.log.Warn("triage_timeout",
		slog.String("dedup_key", dedupKey),
	)
	if err := r.db.UpdateWatcherEventRoutedTo(watcherID, dedupKey, "triage-timeout", ""); err != nil {
		r.log.Warn("triage_timeout_update_failed",
			slog.String("dedup_key", dedupKey),
			slog.String("error", err.Error()),
		)
	}
	r.removeBirth(dedupKey)
}

// removeBirth removes the birth entry for the given dedupKey.
func (r *triageReaper) removeBirth(dedupKey string) {
	r.birthMu.Lock()
	defer r.birthMu.Unlock()
	delete(r.birth, dedupKey)
}
