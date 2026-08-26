package watcher

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cloud.google.com/go/pubsub" //nolint:staticcheck // SA1019: pubsub v1 intentional per Plan 17-02; v2 migration deferred
	"cloud.google.com/go/pubsub/pstest"
	"golang.org/x/oauth2"
	gmail "google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/RishabhKodes/agent-deck/internal/session"
)

// ---------- Test helpers ----------

// fakeGmailServer is an httptest server that fakes the Gmail v1 REST API for
// unit tests. It counts /watch POSTs, records the query parameters of the
// most-recent /history GET, and serves fixture responses from testdata/gmail/.
type fakeGmailServer struct {
	srv               *httptest.Server
	mu                sync.Mutex
	watchCalls        int
	historyCalls      int
	lastHistoryQuery  string // raw URL query from most recent /history call
	historyResponseFn func(w http.ResponseWriter, r *http.Request)
}

func newFakeGmailServer(t *testing.T) *fakeGmailServer {
	t.Helper()
	fs := &fakeGmailServer{}
	fs.historyResponseFn = func(w http.ResponseWriter, r *http.Request) {
		data, err := os.ReadFile("testdata/gmail/history_list.json")
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
	}
	fs.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case strings.HasSuffix(path, "/watch"):
			fs.mu.Lock()
			fs.watchCalls++
			fs.mu.Unlock()
			data, err := os.ReadFile("testdata/gmail/watch_response.json")
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(data)
		case strings.HasSuffix(path, "/stop"):
			w.WriteHeader(http.StatusNoContent)
		case strings.Contains(path, "/history"):
			fs.mu.Lock()
			fs.historyCalls++
			fs.lastHistoryQuery = r.URL.RawQuery
			fn := fs.historyResponseFn
			fs.mu.Unlock()
			if fn != nil {
				fn(w, r)
				return
			}
			w.WriteHeader(500)
		case strings.Contains(path, "/messages/"):
			data, err := os.ReadFile("testdata/gmail/message_metadata.json")
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(data)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(func() { fs.srv.Close() })
	return fs
}

func (fs *fakeGmailServer) URL() string {
	return fs.srv.URL
}

func (fs *fakeGmailServer) WatchCalls() int {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.watchCalls
}

func (fs *fakeGmailServer) HistoryCalls() int {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.historyCalls
}

func (fs *fakeGmailServer) LastHistoryQuery() string {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.lastHistoryQuery
}

func (fs *fakeGmailServer) SetHistoryResponse(fn func(w http.ResponseWriter, r *http.Request)) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.historyResponseFn = fn
}

// newFakePubSub spins up a pstest-backed pubsub.Client with one topic + one
// subscription and wires Cleanup so goleak stays green.
func newFakePubSub(t *testing.T) (*pstest.Server, *pubsub.Client, *pubsub.Topic, *pubsub.Subscription) {
	t.Helper()
	ctx := context.Background()
	srv := pstest.NewServer()
	t.Cleanup(func() { _ = srv.Close() })

	conn, err := grpc.NewClient(srv.Addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	client, err := pubsub.NewClient(ctx, "test-project",
		option.WithGRPCConn(conn))
	if err != nil {
		t.Fatalf("pubsub.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	topic, err := client.CreateTopic(ctx, "gmail-test")
	if err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}
	sub, err := client.CreateSubscription(ctx, "gmail-test-sub",
		pubsub.SubscriptionConfig{Topic: topic})
	if err != nil {
		t.Fatalf("CreateSubscription: %v", err)
	}
	return srv, client, topic, sub
}

// seedFakeOAuth points HOME at a t.TempDir() and writes the credentials.json +
// token.json fixtures into ~/.agent-deck/watcher/<name>/. Returns the
// watcher's on-disk directory (with meta.json later written there).
func seedFakeOAuth(t *testing.T, watcherName string) string {
	t.Helper()
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	dir := filepath.Join(tmpDir, ".agent-deck", "watchers", watcherName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir watcher dir: %v", err)
	}
	creds, err := os.ReadFile("testdata/gmail/credentials.json")
	if err != nil {
		t.Fatalf("read credentials fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "credentials.json"), creds, 0o600); err != nil {
		t.Fatalf("write credentials: %v", err)
	}
	tok, err := os.ReadFile("testdata/gmail/token.json")
	if err != nil {
		t.Fatalf("read token fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "token.json"), tok, 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}
	return dir
}

// publishEnvelope publishes a synthetic Gmail Pub/Sub envelope to the given
// topic and blocks until the publish ack is received.
func publishEnvelope(t *testing.T, topic *pubsub.Topic, email string, historyID uint64) {
	t.Helper()
	ctx := context.Background()
	payload := map[string]any{
		"emailAddress": email,
		"historyId":    fmt.Sprintf("%d", historyID),
	}
	data, _ := json.Marshal(payload)
	result := topic.Publish(ctx, &pubsub.Message{Data: data})
	if _, err := result.Get(ctx); err != nil {
		t.Fatalf("publish: %v", err)
	}
}

// ---------- Test 1: Setup registers watch when meta.json is missing ----------

func TestGmailAdapter_Setup_RegistersWatchWhenMissing(t *testing.T) {
	watcherName := "gmail-test-setup-register"
	seedFakeOAuth(t, watcherName)

	// Fake Gmail HTTP server (counts /watch POSTs).
	fs := newFakeGmailServer(t)

	// Fake Pub/Sub backend.
	_, psClient, _, sub := newFakePubSub(t)

	// We need Setup to use our fake Gmail endpoint + our pre-built pubsub
	// client. The production Setup builds its own clients from the OAuth
	// config — it calls pubsub.NewClient with the real endpoint, which would
	// hang on ctx deadline. For this test we exercise Setup's control flow
	// (credential load, meta.json check, registerWatch) directly rather than
	// via the full flow. A follow-up integration test in Plan 17-04 covers
	// the full Setup path against a stubbed pubsub endpoint.
	a := newGmailAdapterForTest(watcherName, fs.URL(), psClient, sub)
	// Mirror the subset of Setup that this test validates: load meta (absent),
	// then enter the D-11 threshold branch.
	a.nowFunc = time.Now

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := a.registerWatch(ctx); err != nil {
		t.Fatalf("registerWatch: %v", err)
	}

	if got := fs.WatchCalls(); got != 1 {
		t.Errorf("expected exactly 1 users.Watch call, got %d", got)
	}

	// meta.json should now contain a non-empty WatchExpiry.
	meta, err := session.LoadWatcherMeta(watcherName)
	if err != nil {
		t.Fatalf("LoadWatcherMeta: %v", err)
	}
	if meta.WatchExpiry == "" {
		t.Fatalf("expected meta.WatchExpiry to be set after registerWatch, got empty")
	}
	if _, err := time.Parse(time.RFC3339, meta.WatchExpiry); err != nil {
		t.Errorf("meta.WatchExpiry is not RFC3339: %q (%v)", meta.WatchExpiry, err)
	}
	if meta.WatchHistoryID == "" {
		t.Errorf("expected meta.WatchHistoryID to be set, got empty")
	}
}

// ---------- Test 2: Receive processes envelope end-to-end via pstest + httptest ----------

func TestGmailAdapter_Receive_ProcessesEnvelope(t *testing.T) {
	watcherName := "gmail-test-receive-envelope"
	seedFakeOAuth(t, watcherName)

	fs := newFakeGmailServer(t)
	_, psClient, topic, sub := newFakePubSub(t)

	a := newGmailAdapterForTest(watcherName, fs.URL(), psClient, sub)
	// Start with no persisted historyID so the handler falls back to the envelope's.
	a.watchHistoryID = 0
	// Prevent the renewalLoop (now real in Plan 17-03) from racing with
	// envelope processing. Pin expiry far in future, inject never-firing
	// afterFunc so the goroutine parks on ctx.Done.
	a.watchExpiry = time.Now().Add(30 * 24 * time.Hour)
	a.afterFunc = func(time.Duration) <-chan time.Time { return make(chan time.Time) }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events := make(chan Event, 4)
	listenDone := make(chan error, 1)
	go func() {
		listenDone <- a.Listen(ctx, events)
	}()

	publishEnvelope(t, topic, "alice@example.com", 1001)

	select {
	case evt := <-events:
		if evt.Source != "gmail" {
			t.Errorf("Source = %q, want gmail", evt.Source)
		}
		if evt.Sender != "alice@example.com" {
			t.Errorf("Sender = %q, want alice@example.com", evt.Sender)
		}
		if evt.Subject != "Test Email Subject" {
			t.Errorf("Subject = %q, want Test Email Subject", evt.Subject)
		}
		if evt.Body != "Hello from the test fixture" {
			t.Errorf("Body = %q, want Hello from the test fixture", evt.Body)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for event")
	}

	cancel()
	select {
	case <-listenDone:
	case <-time.After(5 * time.Second):
		t.Fatalf("Listen did not return after cancel")
	}
}

// ---------- Test 3: Receive calls history.list with persisted startHistoryId ----------

func TestGmailAdapter_Receive_CallsHistoryList(t *testing.T) {
	watcherName := "gmail-test-history-startid"
	seedFakeOAuth(t, watcherName)

	fs := newFakeGmailServer(t)
	_, psClient, topic, sub := newFakePubSub(t)

	a := newGmailAdapterForTest(watcherName, fs.URL(), psClient, sub)
	a.watchHistoryID = 500 // persisted value — history.list should use this, NOT envelope's 1001
	// Prevent the renewalLoop (now real in Plan 17-03) from firing and
	// overwriting watchHistoryID via registerWatch. Pin expiry far in the
	// future and inject a never-firing afterFunc.
	a.watchExpiry = time.Now().Add(30 * 24 * time.Hour)
	a.afterFunc = func(time.Duration) <-chan time.Time { return make(chan time.Time) }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events := make(chan Event, 4)
	listenDone := make(chan error, 1)
	go func() {
		listenDone <- a.Listen(ctx, events)
	}()

	publishEnvelope(t, topic, "alice@example.com", 1001)

	// Wait for at least one history call OR one event delivery.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if fs.HistoryCalls() > 0 {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}

	if fs.HistoryCalls() == 0 {
		cancel()
		<-listenDone
		t.Fatalf("expected at least one /history call, got 0")
	}
	query := fs.LastHistoryQuery()
	// The Gmail client encodes startHistoryId as a query param.
	if !strings.Contains(query, "startHistoryId=500") {
		t.Errorf("expected query to contain startHistoryId=500, got %q", query)
	}

	cancel()
	select {
	case <-listenDone:
	case <-time.After(5 * time.Second):
		t.Fatalf("Listen did not return after cancel")
	}
}

// ---------- Test 4: Stale historyId 404 fallback ----------

func TestGmailAdapter_Receive_StaleHistoryFallback(t *testing.T) {
	watcherName := "gmail-test-stale-history"
	seedFakeOAuth(t, watcherName)

	fs := newFakeGmailServer(t)
	// Make /history return 404 — mimics Gmail's "historyId is invalid" response.
	fs.SetHistoryResponse(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":404,"message":"Requested entity was not found."}}`))
	})

	pstestSrv, psClient, topic, sub := newFakePubSub(t)

	a := newGmailAdapterForTest(watcherName, fs.URL(), psClient, sub)
	a.watchHistoryID = 500 // too-old
	// Prevent renewalLoop from racing with the fallback logic.
	a.watchExpiry = time.Now().Add(30 * 24 * time.Hour)
	a.afterFunc = func(time.Duration) <-chan time.Time { return make(chan time.Time) }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events := make(chan Event, 4)
	listenDone := make(chan error, 1)
	go func() {
		listenDone <- a.Listen(ctx, events)
	}()

	publishEnvelope(t, topic, "alice@example.com", 2000)

	// Wait until at least one history call completes and the message is Acked.
	deadline := time.Now().Add(5 * time.Second)
	var acked bool
	for time.Now().Before(deadline) {
		if fs.HistoryCalls() > 0 {
			// Inspect pstest to see if the message was Acked.
			msgs := pstestSrv.Messages()
			for _, m := range msgs {
				if m.Acks > 0 {
					acked = true
					break
				}
			}
			if acked {
				break
			}
		}
		time.Sleep(25 * time.Millisecond)
	}

	if fs.HistoryCalls() == 0 {
		cancel()
		<-listenDone
		t.Fatalf("expected at least one /history call")
	}
	if !acked {
		cancel()
		<-listenDone
		t.Fatalf("expected Pub/Sub message to be Acked after 404 fallback")
	}

	// After fallback, in-memory watchHistoryID should be the envelope's 2000.
	a.mu.Lock()
	got := a.watchHistoryID
	a.mu.Unlock()
	if got != 2000 {
		t.Errorf("expected watchHistoryID=2000 after fallback, got %d", got)
	}

	cancel()
	select {
	case <-listenDone:
	case <-time.After(5 * time.Second):
		t.Fatalf("Listen did not return after cancel")
	}
}

// ---------- Test 5: normalizeGmailMessage extracts headers + metadata ----------

func TestGmailAdapter_NormalizeMessage(t *testing.T) {
	msg := &gmail.Message{
		Id:           "msg-001",
		ThreadId:     "thr-001",
		LabelIds:     []string{"INBOX"},
		Snippet:      "hello",
		InternalDate: 1712345678000,
		Payload: &gmail.MessagePart{
			MimeType: "text/plain",
			Headers: []*gmail.MessagePartHeader{
				{Name: "From", Value: "Alice Example <alice@example.com>"},
				{Name: "Subject", Value: "Test"},
				{Name: "Date", Value: "Tue, 09 Apr 2024 12:34:56 +0000"},
			},
		},
	}

	evt := normalizeGmailMessage(msg)

	if evt.Source != "gmail" {
		t.Errorf("Source = %q, want gmail", evt.Source)
	}
	if evt.Sender != "alice@example.com" {
		t.Errorf("Sender = %q, want alice@example.com (display name stripped)", evt.Sender)
	}
	if evt.Subject != "Test" {
		t.Errorf("Subject = %q, want Test", evt.Subject)
	}
	if evt.Body != "hello" {
		t.Errorf("Body = %q, want hello", evt.Body)
	}
	wantTS := time.UnixMilli(1712345678000).UTC()
	if !evt.Timestamp.Equal(wantTS) {
		t.Errorf("Timestamp = %v, want %v", evt.Timestamp, wantTS)
	}
	if len(evt.RawPayload) == 0 {
		t.Errorf("RawPayload should be non-empty")
	}
}

// ---------- Test 6: Label filter ----------

func TestGmailAdapter_LabelFilter(t *testing.T) {
	a := &GmailAdapter{
		labels: map[string]struct{}{
			"INBOX":     {},
			"IMPORTANT": {},
		},
	}

	if a.passesLabelFilter([]string{"DRAFT"}) {
		t.Error("expected passesLabelFilter([DRAFT]) = false (no intersection)")
	}
	if !a.passesLabelFilter([]string{"INBOX", "DRAFT"}) {
		t.Error("expected passesLabelFilter([INBOX, DRAFT]) = true (INBOX intersects)")
	}

	// Empty filter (nil labels map) accepts everything.
	empty := &GmailAdapter{}
	if !empty.passesLabelFilter([]string{}) {
		t.Error("empty filter should accept empty label list")
	}
	if !empty.passesLabelFilter([]string{"DRAFT"}) {
		t.Error("empty filter should accept non-empty label list")
	}
}

// ---------- Test 7: WatchExpiry persisted in meta.json ----------

func TestGmailAdapter_PersistsWatchExpiry(t *testing.T) {
	watcherName := "gmail-test-persist-expiry"
	seedFakeOAuth(t, watcherName)

	fs := newFakeGmailServer(t)
	_, psClient, _, sub := newFakePubSub(t)
	a := newGmailAdapterForTest(watcherName, fs.URL(), psClient, sub)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := a.registerWatch(ctx); err != nil {
		t.Fatalf("registerWatch: %v", err)
	}

	meta, err := session.LoadWatcherMeta(watcherName)
	if err != nil {
		t.Fatalf("LoadWatcherMeta: %v", err)
	}
	if meta.WatchExpiry == "" {
		t.Fatalf("WatchExpiry is empty")
	}
	parsed, err := time.Parse(time.RFC3339, meta.WatchExpiry)
	if err != nil {
		t.Fatalf("WatchExpiry %q is not RFC3339: %v", meta.WatchExpiry, err)
	}
	// watch_response.json expiration = 4102444800000 ms = 2100-01-01 UTC.
	want := time.UnixMilli(4102444800000).UTC().Format(time.RFC3339)
	if meta.WatchExpiry != want {
		t.Errorf("WatchExpiry = %q, want %q", meta.WatchExpiry, want)
	}
	// And the parsed time should be far in the future.
	if parsed.Before(time.Now().Add(24 * time.Hour)) {
		t.Errorf("WatchExpiry %v should be far in the future", parsed)
	}
	if meta.WatchHistoryID != "1000" {
		t.Errorf("WatchHistoryID = %q, want 1000", meta.WatchHistoryID)
	}
}

// ---------- Test 8: WatchHistoryID persisted with 5s throttle ----------

func TestGmailAdapter_PersistsHistoryIDThrottled(t *testing.T) {
	watcherName := "gmail-test-throttle"
	_ = seedFakeOAuth(t, watcherName)

	// Seed a pre-existing meta.json so LoadWatcherMeta returns non-nil
	// (preserves CreatedAt).
	base := &session.WatcherMeta{
		Name:      watcherName,
		Type:      "gmail",
		CreatedAt: "2026-04-11T00:00:00Z",
	}
	if err := session.SaveWatcherMeta(base); err != nil {
		t.Fatalf("seed meta: %v", err)
	}

	a := NewGmailAdapter()
	a.name = watcherName

	// Controllable clock.
	var fakeNow atomic.Int64
	fakeNow.Store(time.Date(2026, 4, 11, 12, 0, 0, 0, time.UTC).UnixNano())
	a.nowFunc = func() time.Time { return time.Unix(0, fakeNow.Load()).UTC() }

	// First call: should write meta.json.
	a.mu.Lock()
	a.watchHistoryID = 100
	a.watchExpiry = time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC)
	a.mu.Unlock()
	a.persistHistoryIDThrottled()

	meta1, err := session.LoadWatcherMeta(watcherName)
	if err != nil {
		t.Fatalf("LoadWatcherMeta 1: %v", err)
	}
	if meta1.WatchHistoryID != "100" {
		t.Fatalf("first write: WatchHistoryID = %q, want 100", meta1.WatchHistoryID)
	}

	// Advance clock by 1s (within throttle).
	fakeNow.Add(int64(time.Second))

	// Second call with a NEW id — throttle MUST block the write.
	a.mu.Lock()
	a.watchHistoryID = 200
	a.mu.Unlock()
	a.persistHistoryIDThrottled()

	meta2, err := session.LoadWatcherMeta(watcherName)
	if err != nil {
		t.Fatalf("LoadWatcherMeta 2: %v", err)
	}
	if meta2.WatchHistoryID != "100" {
		t.Errorf("within throttle: WatchHistoryID = %q, want 100 (throttled)", meta2.WatchHistoryID)
	}

	// Advance clock by another 6s (past throttle).
	fakeNow.Add(int64(6 * time.Second))

	a.persistHistoryIDThrottled()

	meta3, err := session.LoadWatcherMeta(watcherName)
	if err != nil {
		t.Fatalf("LoadWatcherMeta 3: %v", err)
	}
	if meta3.WatchHistoryID != "200" {
		t.Errorf("past throttle: WatchHistoryID = %q, want 200", meta3.WatchHistoryID)
	}
}

// ---------- Test 9: OAuth persistingTokenSource full exercise (Plan 17-03) ----------

// tokenSourceFunc adapts a function to the oauth2.TokenSource interface for testing.
type tokenSourceFunc func() (*oauth2.Token, error)

func (f tokenSourceFunc) Token() (*oauth2.Token, error) { return f() }

func TestGmailAdapter_OAuth_PersistsRefreshedToken(t *testing.T) {
	tmpDir := t.TempDir()
	tokenPath := filepath.Join(tmpDir, "token.json")

	initial := &oauth2.Token{
		AccessToken:  "initial-access-token",
		TokenType:    "Bearer",
		RefreshToken: "fake-refresh-token",
		Expiry:       time.Now().Add(time.Hour),
	}
	initialJSON, err := json.MarshalIndent(initial, "", "  ")
	if err != nil {
		t.Fatalf("marshal initial: %v", err)
	}
	if err := os.WriteFile(tokenPath, initialJSON, 0o600); err != nil {
		t.Fatal(err)
	}

	// Custom TokenSource: first Token() call returns initial, second returns
	// a refreshed token (simulates oauth2's refresh-on-expiry behavior).
	var callCount int
	var callMu sync.Mutex
	refreshed := &oauth2.Token{
		AccessToken:  "refreshed-access-token",
		TokenType:    "Bearer",
		RefreshToken: "fake-refresh-token",
		Expiry:       time.Now().Add(2 * time.Hour),
	}
	refreshedSource := tokenSourceFunc(func() (*oauth2.Token, error) {
		callMu.Lock()
		defer callMu.Unlock()
		callCount++
		if callCount == 1 {
			return initial, nil
		}
		return refreshed, nil
	})

	// Wrap directly (not via newPersistingTokenSource) so we bypass
	// ReuseTokenSource's caching for this test.
	pts := &persistingTokenSource{
		inner: refreshedSource,
		path:  tokenPath,
		last:  initial,
	}

	// First call — returns initial, disk content unchanged (access token matches).
	tok1, err := pts.Token()
	if err != nil {
		t.Fatalf("first Token(): %v", err)
	}
	if tok1.AccessToken != "initial-access-token" {
		t.Errorf("first Token() AccessToken = %q, want initial-access-token", tok1.AccessToken)
	}

	// Second call — returns refreshed, MUST rewrite disk atomically.
	tok2, err := pts.Token()
	if err != nil {
		t.Fatalf("second Token(): %v", err)
	}
	if tok2.AccessToken != "refreshed-access-token" {
		t.Errorf("second Token() AccessToken = %q, want refreshed-access-token", tok2.AccessToken)
	}

	// Verify token.json was rewritten.
	persistedBytes, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatalf("read token.json: %v", err)
	}
	var persistedTok oauth2.Token
	if err := json.Unmarshal(persistedBytes, &persistedTok); err != nil {
		t.Fatalf("unmarshal persisted token: %v", err)
	}
	if persistedTok.AccessToken != "refreshed-access-token" {
		t.Errorf("persisted AccessToken = %q, want refreshed-access-token", persistedTok.AccessToken)
	}

	// Verify file mode is 0600 (T-17-05 / T-17-17 mitigation).
	info, err := os.Stat(tokenPath)
	if err != nil {
		t.Fatalf("stat token.json: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("token.json mode = %o, want 0600", mode)
	}

	// Verify no .tmp leaked.
	if _, err := os.Stat(tokenPath + ".tmp"); err == nil {
		t.Error("token.json.tmp leaked after successful persist")
	}
}

// ---------- Plan 17-03 Task 3: HealthCheck branch tests ----------

// TestGmailAdapter_HealthCheck_InvalidToken verifies HealthCheck surfaces token
// refresh failures (expired refresh token, revoked grant) instead of silently
// dropping events. Success criterion 4 from 17-CONTEXT.md.
func TestGmailAdapter_HealthCheck_InvalidToken(t *testing.T) {
	a := NewGmailAdapter()
	a.name = "gmail-test-invalid-token"
	a.tokenSrc = tokenSourceFunc(func() (*oauth2.Token, error) {
		return nil, fmt.Errorf("invalid grant: refresh token expired")
	})
	// Far-future expiry so the expiry branch does NOT fire — isolate the token branch.
	a.watchExpiry = time.Now().Add(7 * 24 * time.Hour)

	err := a.HealthCheck()
	if err == nil {
		t.Fatal("HealthCheck should return error when token source fails")
	}
	lower := strings.ToLower(err.Error())
	if !strings.Contains(lower, "invalid grant") && !strings.Contains(lower, "token") {
		t.Errorf("HealthCheck error should mention token failure, got: %v", err)
	}
}

// TestGmailAdapter_HealthCheck_MissingSubscription verifies HealthCheck fails
// when the Pub/Sub subscription no longer exists (user deleted it, project
// rotated, etc). Success criterion 4.
func TestGmailAdapter_HealthCheck_MissingSubscription(t *testing.T) {
	_, client, _, sub := newFakePubSub(t)

	// Delete the subscription so Exists() returns false.
	ctx := context.Background()
	if err := sub.Delete(ctx); err != nil {
		t.Fatalf("delete subscription: %v", err)
	}

	a := NewGmailAdapter()
	a.name = "gmail-test-missing-sub"
	a.subscr = "projects/test-project/subscriptions/gmail-test-sub"
	a.pubsubClient = client
	a.subscription = client.Subscription("gmail-test-sub")
	a.tokenSrc = tokenSourceFunc(func() (*oauth2.Token, error) {
		return &oauth2.Token{AccessToken: "fake", Expiry: time.Now().Add(time.Hour)}, nil
	})
	a.watchExpiry = time.Now().Add(7 * 24 * time.Hour)

	err := a.HealthCheck()
	if err == nil {
		t.Fatal("HealthCheck should return error when subscription missing")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "subscription") {
		t.Errorf("HealthCheck error should mention subscription, got: %v", err)
	}
}

// TestGmailAdapter_HealthCheck_ExpiredWatch verifies HealthCheck fails when the
// in-memory WatchExpiry has lapsed — catches the case where the renewal
// goroutine has permanently failed and the 7-day watch is dead. Success
// criterion 4.
func TestGmailAdapter_HealthCheck_ExpiredWatch(t *testing.T) {
	a := NewGmailAdapter()
	a.name = "gmail-test-expired-watch"
	a.tokenSrc = tokenSourceFunc(func() (*oauth2.Token, error) {
		return &oauth2.Token{AccessToken: "fake", Expiry: time.Now().Add(time.Hour)}, nil
	})
	// Watch expired 1h ago.
	a.watchExpiry = time.Now().Add(-1 * time.Hour)
	// Leave pubsubClient/subscription nil so the subscription branch is skipped.

	err := a.HealthCheck()
	if err == nil {
		t.Fatal("HealthCheck should return error when watch expiry lapsed")
	}
	lower := strings.ToLower(err.Error())
	if !strings.Contains(lower, "expiry") && !strings.Contains(lower, "lapsed") {
		t.Errorf("HealthCheck error should mention expiry/lapsed, got: %v", err)
	}
}

// ---------- Plan 17-03 Task 2: Renewal tests ----------

// TestGmailAdapter_Setup_RenewsWhenWithin2Hours verifies the D-11 startup
// threshold: when meta.json has a WatchExpiry within 2 hours of now, Setup
// re-registers the watch. Exercises the extracted maybeRenewOnStartup helper
// directly since the full Setup path requires real OAuth + pubsub wiring.
func TestGmailAdapter_Setup_RenewsWhenWithin2Hours(t *testing.T) {
	watcherName := "gmail-test-renew-within-2h"
	seedFakeOAuth(t, watcherName)

	// Pre-seed meta.json with an expiry 90 minutes in the future — inside the 2h threshold.
	originalExpiry := time.Now().Add(90 * time.Minute).UTC().Format(time.RFC3339)
	seedMeta := &session.WatcherMeta{
		Name:           watcherName,
		Type:           "gmail",
		CreatedAt:      time.Now().UTC().Format(time.RFC3339),
		WatchExpiry:    originalExpiry,
		WatchHistoryID: "500",
	}
	if err := session.SaveWatcherMeta(seedMeta); err != nil {
		t.Fatalf("seed meta: %v", err)
	}

	fs := newFakeGmailServer(t)
	_, psClient, _, sub := newFakePubSub(t)

	a := newGmailAdapterForTest(watcherName, fs.URL(), psClient, sub)
	// Mirror the subset of Setup that produces the state maybeRenewOnStartup consumes:
	// load persisted WatchExpiry into memory.
	parsed, err := time.Parse(time.RFC3339, originalExpiry)
	if err != nil {
		t.Fatalf("parse seed expiry: %v", err)
	}
	a.watchExpiry = parsed

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := a.maybeRenewOnStartup(ctx); err != nil {
		t.Fatalf("maybeRenewOnStartup: %v", err)
	}

	if got := fs.WatchCalls(); got != 1 {
		t.Errorf("expected exactly 1 users.Watch call (within 2h threshold), got %d", got)
	}

	meta, err := session.LoadWatcherMeta(watcherName)
	if err != nil {
		t.Fatalf("LoadWatcherMeta: %v", err)
	}
	// watch_response.json expiration = 4102444800000 ms = 2100-01-01 UTC — should differ from the seed.
	want := time.UnixMilli(4102444800000).UTC().Format(time.RFC3339)
	if meta.WatchExpiry != want {
		t.Errorf("WatchExpiry after renew = %q, want %q", meta.WatchExpiry, want)
	}
	if meta.WatchExpiry == originalExpiry {
		t.Error("WatchExpiry was not updated by renewal")
	}
}

// TestGmailAdapter_Setup_NoRenewWhenFresh verifies that when meta.json has a
// WatchExpiry more than 2 hours in the future, Setup does NOT re-register the
// watch (quota-efficient: only renew when necessary).
func TestGmailAdapter_Setup_NoRenewWhenFresh(t *testing.T) {
	watcherName := "gmail-test-no-renew-fresh"
	seedFakeOAuth(t, watcherName)

	// 3h in the future — well past the 2h threshold.
	freshExpiry := time.Now().Add(3 * time.Hour).UTC().Format(time.RFC3339)
	seedMeta := &session.WatcherMeta{
		Name:           watcherName,
		Type:           "gmail",
		CreatedAt:      time.Now().UTC().Format(time.RFC3339),
		WatchExpiry:    freshExpiry,
		WatchHistoryID: "500",
	}
	if err := session.SaveWatcherMeta(seedMeta); err != nil {
		t.Fatalf("seed meta: %v", err)
	}

	fs := newFakeGmailServer(t)
	_, psClient, _, sub := newFakePubSub(t)

	a := newGmailAdapterForTest(watcherName, fs.URL(), psClient, sub)
	parsed, err := time.Parse(time.RFC3339, freshExpiry)
	if err != nil {
		t.Fatalf("parse seed expiry: %v", err)
	}
	a.watchExpiry = parsed

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := a.maybeRenewOnStartup(ctx); err != nil {
		t.Fatalf("maybeRenewOnStartup: %v", err)
	}

	if got := fs.WatchCalls(); got != 0 {
		t.Errorf("expected 0 users.Watch calls (>2h threshold), got %d", got)
	}

	meta, err := session.LoadWatcherMeta(watcherName)
	if err != nil {
		t.Fatalf("LoadWatcherMeta: %v", err)
	}
	if meta.WatchExpiry != freshExpiry {
		t.Errorf("WatchExpiry changed: got %q, want %q (should be untouched)", meta.WatchExpiry, freshExpiry)
	}
}

// TestGmailAdapter_RenewalLoop_FiresOnceBeforeExpiry verifies that renewalLoop
// calls registerWatch at watchExpiry-1h, using the injectable afterFunc to
// trigger the wait deterministically.
func TestGmailAdapter_RenewalLoop_FiresOnceBeforeExpiry(t *testing.T) {
	watcherName := "gmail-test-renew-fires"
	seedFakeOAuth(t, watcherName)

	fs := newFakeGmailServer(t)
	_, psClient, _, sub := newFakePubSub(t)

	a := newGmailAdapterForTest(watcherName, fs.URL(), psClient, sub)
	a.topic = "projects/test-project/topics/gmail-test"
	a.subscr = "projects/test-project/subscriptions/gmail-test-sub"

	fakeNow := time.Now()
	fakeAfterCh := make(chan time.Time, 4)
	a.nowFunc = func() time.Time { return fakeNow }
	a.afterFunc = func(d time.Duration) <-chan time.Time { return fakeAfterCh }

	// watchExpiry 1h in the future → renewAt == fakeNow → wait == 0, loop
	// immediately enters the select and consumes the next channel value.
	a.watchExpiry = fakeNow.Add(1 * time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		a.renewalLoop(ctx)
		close(done)
	}()

	// Trigger the first wait select.
	fakeAfterCh <- fakeNow

	// Poll for registerWatch to execute.
	deadline := time.Now().Add(2 * time.Second)
	for fs.WatchCalls() < 1 {
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatalf("registerWatch never called; watchCalls=%d", fs.WatchCalls())
		}
		time.Sleep(5 * time.Millisecond)
	}

	// After success, loop re-reads watchExpiry (now ~2100) and recomputes wait.
	// We cancel to unblock the next select on ctx.Done.
	cancel()
	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("renewalLoop did not exit on ctx cancel")
	}

	if got := fs.WatchCalls(); got != 1 {
		t.Errorf("expected watchCalls==1 after single fire, got %d", got)
	}

	// lastHealthErr should be cleared on success.
	a.mu.Lock()
	lhe := a.lastHealthErr
	a.mu.Unlock()
	if lhe != nil {
		t.Errorf("lastHealthErr after successful renewal = %v, want nil", lhe)
	}
}

// TestGmailAdapter_RenewalLoop_ExitsOnCtxCancel verifies that a renewalLoop
// blocked in the first wait select exits within 200ms of ctx cancellation.
// Never signals afterFunc — the goroutine must take the ctx.Done branch.
func TestGmailAdapter_RenewalLoop_ExitsOnCtxCancel(t *testing.T) {
	a := NewGmailAdapter()
	a.name = "gmail-test-renew-ctx"
	// Never-firing afterFunc so the goroutine parks on the select.
	a.afterFunc = func(d time.Duration) <-chan time.Time {
		return make(chan time.Time) // never delivers
	}
	a.watchExpiry = time.Now().Add(7 * 24 * time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		a.renewalLoop(ctx)
		close(done)
	}()

	// Immediate cancel — goroutine should exit quickly via ctx.Done branch.
	cancel()
	select {
	case <-done:
		// success
	case <-time.After(200 * time.Millisecond):
		t.Fatal("renewalLoop did not exit within 200ms of ctx cancel")
	}
}

// TestGmailAdapter_RenewalLoop_RetryOnFailure verifies that when registerWatch
// fails, renewalLoop stores the error in lastHealthErr, waits 15 minutes (via
// the injectable afterFunc), retries, and clears lastHealthErr on the second
// successful call.
func TestGmailAdapter_RenewalLoop_RetryOnFailure(t *testing.T) {
	watcherName := "gmail-test-renew-retry"
	seedFakeOAuth(t, watcherName)

	// Custom Gmail server: first /watch call returns 500, second returns the fixture.
	var mu sync.Mutex
	var watchCalls int
	tsGmail := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/watch"):
			mu.Lock()
			watchCalls++
			n := watchCalls
			mu.Unlock()
			if n == 1 {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error":{"code":500,"message":"fake failure"}}`))
				return
			}
			data, err := os.ReadFile("testdata/gmail/watch_response.json")
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(data)
		case strings.HasSuffix(r.URL.Path, "/stop"):
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer tsGmail.Close()

	_, psClient, _, sub := newFakePubSub(t)

	a := newGmailAdapterForTest(watcherName, tsGmail.URL, psClient, sub)
	a.topic = "projects/test-project/topics/gmail-test"

	fakeNow := time.Now()
	fakeAfterCh := make(chan time.Time, 4)
	a.nowFunc = func() time.Time { return fakeNow }
	a.afterFunc = func(d time.Duration) <-chan time.Time { return fakeAfterCh }

	// watchExpiry 1h in future → first wait is 0, fires immediately.
	a.watchExpiry = fakeNow.Add(1 * time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		a.renewalLoop(ctx)
		close(done)
	}()

	// Trigger first wait → registerWatch fails with 500.
	fakeAfterCh <- fakeNow

	// Poll until the first /watch call failed and lastHealthErr is set.
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		got := watchCalls
		mu.Unlock()
		if got >= 1 {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatalf("first /watch call never made")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Give the loop a moment to record lastHealthErr and enter the retry select.
	deadline = time.Now().Add(2 * time.Second)
	for {
		a.mu.Lock()
		lhe := a.lastHealthErr
		a.mu.Unlock()
		if lhe != nil {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatalf("lastHealthErr never set after registerWatch failure")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Trigger second wait (the 15-min retry) → loop continues into iter 2.
	// The retry branch takes `continue`, which re-enters the top of the for
	// loop and computes a fresh wait. Since registerWatch failed, watchExpiry
	// is unchanged (fakeNow+1h) and wait is still 0 → the loop immediately
	// enters another first-wait select. We send twice to cover both: the
	// 15-min retry select AND the iter-2 first-wait select.
	fakeAfterCh <- fakeNow
	fakeAfterCh <- fakeNow

	// Poll for the second /watch call.
	deadline = time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		got := watchCalls
		mu.Unlock()
		if got >= 2 {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatalf("second /watch (retry) never made; watchCalls=%d", got)
		}
		time.Sleep(5 * time.Millisecond)
	}

	// After successful retry, lastHealthErr should clear. Poll briefly.
	deadline = time.Now().Add(2 * time.Second)
	for {
		a.mu.Lock()
		lhe := a.lastHealthErr
		a.mu.Unlock()
		if lhe == nil {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatalf("lastHealthErr not cleared after successful retry: %v", lhe)
		}
		time.Sleep(5 * time.Millisecond)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("renewalLoop did not exit on ctx cancel")
	}
}
