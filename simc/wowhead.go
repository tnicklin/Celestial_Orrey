package simc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tnicklin/celestial_orrey/logger"
)

// NameCache is the persistence the resolver uses for item-name lookups.
// Implementations are expected to be safe for concurrent use.
type NameCache interface {
	GetItemName(ctx context.Context, itemID int64) (string, error)
	UpsertItemName(ctx context.Context, itemID int64, name string) error
}

// NameResolver maps a WoW item ID to a human-readable name. It checks
// an in-process LRU first, then the persistent NameCache, then falls back
// to wowhead.com. Misses (network errors, "not found") are returned as
// the empty string so callers can fall back to "id:NNN".
type NameResolver interface {
	Resolve(ctx context.Context, itemID int) string
	// Prime stores a known mapping (e.g. one we extracted from an addon
	// comment) without making a network call.
	Prime(ctx context.Context, itemID int, name string)
}

// WowheadResolver implements NameResolver against wowhead.com.
type WowheadResolver struct {
	cache  NameCache
	client *http.Client
	logger logger.Logger

	mu        sync.RWMutex
	mem       map[int]string
	negativeT map[int]time.Time // items we recently failed to resolve

	// minInterval throttles back-to-back wowhead fetches to be a polite
	// scraper.
	minInterval time.Duration
	lastFetch   time.Time
	fetchMu     sync.Mutex
}

// NewWowheadResolver returns a resolver that uses cache for persistent
// storage and falls back to wowhead.com for unknown IDs.
func NewWowheadResolver(cache NameCache, lg logger.Logger) *WowheadResolver {
	return &WowheadResolver{
		cache:  cache,
		client: &http.Client{Timeout: 10 * time.Second},
		logger: lg,
		mem:    make(map[int]string),
		negativeT:   make(map[int]time.Time),
		minInterval: 250 * time.Millisecond,
	}
}

// Prime records a known item-id→name mapping in memory and the persistent
// cache. Errors talking to the cache are logged at debug level — naming
// is non-critical.
func (r *WowheadResolver) Prime(ctx context.Context, itemID int, name string) {
	if itemID == 0 || strings.TrimSpace(name) == "" {
		return
	}
	r.mu.Lock()
	if existing, ok := r.mem[itemID]; ok && existing == name {
		r.mu.Unlock()
		return
	}
	r.mem[itemID] = name
	r.mu.Unlock()
	if r.cache == nil {
		return
	}
	if err := r.cache.UpsertItemName(ctx, int64(itemID), name); err != nil {
		r.logger.WarnW("cache item name", "id", itemID, "name", name, "error", err)
	}
}

// Resolve returns the cached or freshly-fetched name for itemID, or the
// empty string if it can't be determined.
func (r *WowheadResolver) Resolve(ctx context.Context, itemID int) string {
	if itemID == 0 {
		return ""
	}
	r.mu.RLock()
	if name, ok := r.mem[itemID]; ok {
		r.mu.RUnlock()
		return name
	}
	if t, ok := r.negativeT[itemID]; ok && time.Since(t) < time.Hour {
		r.mu.RUnlock()
		return ""
	}
	r.mu.RUnlock()

	if r.cache != nil {
		if name, err := r.cache.GetItemName(ctx, int64(itemID)); err == nil && name != "" {
			r.mu.Lock()
			r.mem[itemID] = name
			r.mu.Unlock()
			return name
		}
	}

	name, err := r.fetchFromWowhead(ctx, itemID)
	if err != nil || name == "" {
		r.mu.Lock()
		r.negativeT[itemID] = time.Now()
		r.mu.Unlock()
		if err != nil {
			r.logger.WarnW("wowhead lookup", "id", itemID, "error", err)
		}
		return ""
	}

	r.Prime(ctx, itemID, name)
	return name
}

// wowheadTitleRE pulls the item name out of wowhead's <title>:
//   "<title>Brambledawn Halo - Item - World of Warcraft</title>"
var wowheadTitleRE = regexp.MustCompile(`(?is)<title>([^<]+?)\s*-\s*Item\s*-\s*World of Warcraft</title>`)

// wowheadNotFoundRE is wowhead's standard "this item doesn't exist" page.
var wowheadNotFoundRE = regexp.MustCompile(`(?i)Item not found`)

func (r *WowheadResolver) fetchFromWowhead(ctx context.Context, itemID int) (string, error) {
	r.fetchMu.Lock()
	if since := time.Since(r.lastFetch); since < r.minInterval {
		time.Sleep(r.minInterval - since)
	}
	r.lastFetch = time.Now()
	r.fetchMu.Unlock()

	url := "https://www.wowhead.com/item=" + strconv.Itoa(itemID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	// Wowhead returns a generic homepage to clients without a UA.
	req.Header.Set("User-Agent", "celestial_orrey/1.0 (+https://github.com/tnicklin/celestial_orrey)")
	req.Header.Set("Accept", "text/html")

	resp, err := r.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", nil
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("wowhead status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		return "", err
	}
	if wowheadNotFoundRE.Match(body) {
		return "", nil
	}
	if m := wowheadTitleRE.FindSubmatch(body); m != nil {
		return strings.TrimSpace(string(m[1])), nil
	}
	return "", errors.New("title not found in wowhead response")
}

// noopNameResolver is a NameResolver that always returns "". Used when
// no cache is configured.
type noopNameResolver struct{}

// NewNoopNameResolver returns a resolver that always returns "".
func NewNoopNameResolver() NameResolver { return noopNameResolver{} }

func (noopNameResolver) Resolve(_ context.Context, _ int) string { return "" }
func (noopNameResolver) Prime(_ context.Context, _ int, _ string) {}
