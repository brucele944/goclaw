package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// newTestConcurrencyGate builds a ConcurrencyGate with the exact semantics
// cmd.providerConcurrencyGate uses (a semaphore-bounded gate), without
// depending on internal/scheduler from this package: providers must not
// import scheduler (see concurrency.go's package doc), so the test proves the
// gate *contract* — bounded concurrency, block-until-slot, release-on-return —
// that any real implementation (cmd's scheduler.Lane-backed one included) must
// satisfy.
func newTestConcurrencyGate(maxInFlight int) ConcurrencyGate {
	sem := make(chan struct{}, maxInFlight)
	return func(ctx context.Context, fn func()) error {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			return ctx.Err()
		}
		defer func() { <-sem }()
		fn()
		return nil
	}
}

// TestConcurrencyGateSerializesMaxInFlightOne is phase 5's acceptance
// criterion: "max_in_flight=1 serialises two concurrent calls to that
// provider (observable via overlapping request timestamps on the fake
// upstream)." Two Chat() calls are fired concurrently against a fake upstream
// that records [start,end) per request; with max_in_flight=1 the two windows
// must not overlap. This is an ordering assertion, not a throughput/perf
// benchmark.
func TestConcurrencyGateSerializesMaxInFlightOne(t *testing.T) {
	type window struct{ start, end time.Time }
	var mu sync.Mutex
	var windows []window

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		time.Sleep(30 * time.Millisecond) // long enough that a race would overlap the two windows
		end := time.Now()
		mu.Lock()
		windows = append(windows, window{start, end})
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message":       map[string]string{"content": "ok"},
				"finish_reason": "stop",
			}},
		})
	}))
	t.Cleanup(server.Close)

	provider := NewOpenAIProvider("gated", "test-key", server.URL, "gpt-test")
	provider.WithConcurrencyGate(newTestConcurrencyGate(1))

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := provider.Chat(context.Background(), ChatRequest{
				Messages: []Message{{Role: "user", Content: "hello"}},
			})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("Chat() error = %v", err)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(windows) != 2 {
		t.Fatalf("upstream received %d requests, want 2", len(windows))
	}
	a, b := windows[0], windows[1]
	if a.end.After(b.start) && b.end.After(a.start) {
		t.Fatalf("requests overlapped despite max_in_flight=1: a=[%v,%v) b=[%v,%v)", a.start, a.end, b.start, b.end)
	}
}

// TestConcurrencyGateNilRunsUnbounded proves the default (no gate configured,
// as for any provider whose settings omit max_in_flight) still runs
// concurrently — the gate must be strictly opt-in.
func TestConcurrencyGateNilRunsUnbounded(t *testing.T) {
	release := make(chan struct{})
	var inFlight int32
	var sawTwoConcurrent bool
	var mu sync.Mutex

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		inFlight++
		if inFlight == 2 {
			sawTwoConcurrent = true
		}
		mu.Unlock()
		<-release
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message":       map[string]string{"content": "ok"},
				"finish_reason": "stop",
			}},
		})
	}))
	t.Cleanup(server.Close)

	provider := NewOpenAIProvider("ungated", "test-key", server.URL, "gpt-test")
	// No WithConcurrencyGate call: concurrencyGate stays nil.

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = provider.Chat(context.Background(), ChatRequest{
				Messages: []Message{{Role: "user", Content: "hello"}},
			})
		}()
	}
	time.Sleep(100 * time.Millisecond) // let both requests reach the handler
	close(release)
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if !sawTwoConcurrent {
		t.Fatal("expected both requests in flight concurrently with no gate configured")
	}
}
