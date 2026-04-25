package simc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tnicklin/celestial_orrey/logger"
)

// fakeNameCache is an in-memory NameCache for tests.
type fakeNameCache struct {
	mu    sync.Mutex
	items map[int64]string
	gets  int
}

func newFakeNameCache() *fakeNameCache {
	return &fakeNameCache{items: map[int64]string{}}
}

func (f *fakeNameCache) GetItemName(_ context.Context, id int64) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gets++
	if v, ok := f.items[id]; ok {
		return v, nil
	}
	return "", nil
}

func (f *fakeNameCache) UpsertItemName(_ context.Context, id int64, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.items[id] = name
	return nil
}

func TestWowheadResolver_PrimeAndResolve(t *testing.T) {
	cache := newFakeNameCache()
	r := NewWowheadResolver(cache, logger.NewNop())
	ctx := context.Background()

	r.Prime(ctx, 12345, "Test Item")
	if got := r.Resolve(ctx, 12345); got != "Test Item" {
		t.Errorf("Resolve after Prime = %q, want %q", got, "Test Item")
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.items[12345] != "Test Item" {
		t.Errorf("cache miss after Prime: %v", cache.items)
	}
}

func TestWowheadResolver_CacheHit(t *testing.T) {
	cache := newFakeNameCache()
	cache.items[212014] = "Some Cached Helm"

	r := NewWowheadResolver(cache, logger.NewNop())
	ctx := context.Background()

	if got := r.Resolve(ctx, 212014); got != "Some Cached Helm" {
		t.Errorf("Resolve from cache = %q, want %q", got, "Some Cached Helm")
	}
}

func TestWowheadResolver_FetchFromHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><title>Brambledawn Halo - Item - World of Warcraft</title></head></html>`))
	}))
	defer srv.Close()

	cache := newFakeNameCache()
	r := NewWowheadResolver(cache, logger.NewNop())
	r.minInterval = 0

	// Hit the test server directly via fetch helper. We don't override
	// the wowhead URL builder, so just exercise the parsing path.
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	req.Header.Set("User-Agent", "test")
	resp, err := r.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body := make([]byte, 1024)
	n, _ := resp.Body.Read(body)
	m := wowheadTitleRE.FindSubmatch(body[:n])
	if m == nil {
		t.Fatalf("title regex did not match: %s", body[:n])
	}
	if got := strings.TrimSpace(string(m[1])); got != "Brambledawn Halo" {
		t.Errorf("title = %q, want %q", got, "Brambledawn Halo")
	}
}

func TestWowheadResolver_NegativeCache(t *testing.T) {
	cache := newFakeNameCache()
	r := NewWowheadResolver(cache, logger.NewNop())

	r.mu.Lock()
	r.negativeT[999] = time.Now()
	r.mu.Unlock()

	if got := r.Resolve(context.Background(), 999); got != "" {
		t.Errorf("Resolve with negative cache = %q, want empty", got)
	}
}

func TestNoopNameResolver(t *testing.T) {
	r := NewNoopNameResolver()
	if got := r.Resolve(context.Background(), 1); got != "" {
		t.Errorf("noop resolve = %q, want empty", got)
	}
	r.Prime(context.Background(), 1, "x")
}
