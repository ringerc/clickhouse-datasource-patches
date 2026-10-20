package plugin

import (
	"strings"
	"sync"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/sqlds/v5"
)

// connectionIdleTTL is how long a forwarded-header connection pool may sit
// unused before it is closed. Pools in active use are refreshed on every
// lookup, so only abandoned ones (e.g. a rotated session cookie) expire.
const connectionIdleTTL = 15 * time.Minute

// defaultKeySuffix matches the suffix sqlds gives the data source's default
// connection key. sqlds requires that entry to stay in the cache; keys for
// per-header pools end in a hex hash, so they cannot collide with it.
const defaultKeySuffix = "-default"

// idleTTLCache is a sqlds.ConnectionCache that closes and removes
// connections that have not been looked up for ttl. sqlds creates one pool per
// distinct set of forwarded header values and never removes them; without
// eviction every new Cookie or dashboard header value leaks a pool.
type idleTTLCache struct {
	ttl time.Duration
	now func() time.Time
	// closeConn is overridable for tests, since sqlds exposes no
	// constructor for a CachedConnection holding a real *sql.DB.
	closeConn func(key string, c sqlds.CachedConnection)

	mu      sync.Mutex
	entries map[string]*idleTTLEntry
	// retired holds connections that a Store overwrote. Concurrent queries
	// that miss the same key each open a pool and the last Store wins, but
	// the losing query may still be about to use its pool, so it is closed
	// after the TTL rather than immediately.
	retired []idleTTLRetired

	stop     chan struct{}
	stopOnce sync.Once
	done     chan struct{}
}

type idleTTLRetired struct {
	key       string
	conn      sqlds.CachedConnection
	retiredAt time.Time
}

type idleTTLEntry struct {
	conn     sqlds.CachedConnection
	lastUsed time.Time
}

func newIdleTTLCache(ttl time.Duration) *idleTTLCache {
	c := &idleTTLCache{
		ttl: ttl,
		now: time.Now,
		closeConn: func(key string, conn sqlds.CachedConnection) {
			if err := conn.Close(); err != nil {
				backend.Logger.Warn("failed to close connection", "key", key, "error", err)
			}
		},
		entries: map[string]*idleTTLEntry{},
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
	go c.run()
	return c
}

func (c *idleTTLCache) run() {
	defer close(c.done)
	interval := c.ttl / 4
	if interval <= 0 {
		interval = time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			c.sweep()
		case <-c.stop:
			return
		}
	}
}

// sweep closes and removes entries idle for longer than the TTL. Closing
// happens outside the lock because *sql.DB.Close waits for running queries.
func (c *idleTTLCache) sweep() {
	cutoff := c.now().Add(-c.ttl)
	expired := map[string]sqlds.CachedConnection{}
	var expiredRetired []idleTTLRetired
	c.mu.Lock()
	kept := c.retired[:0]
	for _, r := range c.retired {
		if r.retiredAt.Before(cutoff) {
			expiredRetired = append(expiredRetired, r)
		} else {
			kept = append(kept, r)
		}
	}
	c.retired = kept
	for key, e := range c.entries {
		if strings.HasSuffix(key, defaultKeySuffix) {
			continue
		}
		if e.lastUsed.Before(cutoff) {
			expired[key] = e.conn
			delete(c.entries, key)
		}
	}
	c.mu.Unlock()
	for key, conn := range expired {
		c.closeConn(key, conn)
	}
	for _, r := range expiredRetired {
		c.closeConn(r.key, r.conn)
	}
}

func (c *idleTTLCache) Load(key string) (sqlds.CachedConnection, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok {
		return sqlds.CachedConnection{}, false
	}
	e.lastUsed = c.now()
	return e.conn, true
}

func (c *idleTTLCache) Store(key string, v sqlds.CachedConnection) {
	c.mu.Lock()
	now := c.now()
	if old, ok := c.entries[key]; ok && old.conn.DB() != v.DB() {
		c.retired = append(c.retired, idleTTLRetired{key: key, conn: old.conn, retiredAt: now})
	}
	c.entries[key] = &idleTTLEntry{conn: v, lastUsed: now}
	c.mu.Unlock()
}

func (c *idleTTLCache) Range(f func(key string, v sqlds.CachedConnection) bool) {
	c.mu.Lock()
	snapshot := make(map[string]sqlds.CachedConnection, len(c.entries))
	for k, e := range c.entries {
		snapshot[k] = e.conn
	}
	c.mu.Unlock()
	for k, v := range snapshot {
		if !f(k, v) {
			return
		}
	}
}

// Dispose stops the sweeper and closes every remaining connection.
func (c *idleTTLCache) Dispose() {
	c.stopOnce.Do(func() { close(c.stop) })
	<-c.done
	c.mu.Lock()
	remaining := c.entries
	retired := c.retired
	c.entries = map[string]*idleTTLEntry{}
	c.retired = nil
	c.mu.Unlock()
	for key, e := range remaining {
		c.closeConn(key, e.conn)
	}
	for _, r := range retired {
		c.closeConn(r.key, r.conn)
	}
}
