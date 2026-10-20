package plugin

import (
	"context"
	"testing"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/sqlds/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestCache returns a cache with a controllable clock and a close hook
// that records the keys it was called for.
func newTestCache(ttl time.Duration) (c *idleTTLCache, now *time.Time, closed *[]string) {
	c = newIdleTTLCache(ttl)
	t := time.Now()
	now = &t
	c.now = func() time.Time { return *now }
	closed = &[]string{}
	c.closeConn = func(key string, _ sqlds.CachedConnection) { *closed = append(*closed, key) }
	return c, now, closed
}

func TestIdleTTLCacheEvictsIdleEntriesAndClosesThem(t *testing.T) {
	c, now, closed := newTestCache(time.Minute)
	defer c.Dispose()

	c.Store("uid-abc", sqlds.CachedConnection{})
	_, ok := c.Load("uid-abc")
	require.True(t, ok)

	*now = now.Add(2 * time.Minute)
	c.sweep()

	_, ok = c.Load("uid-abc")
	assert.False(t, ok)
	assert.Equal(t, []string{"uid-abc"}, *closed)
}

func TestIdleTTLCacheLoadRefreshesIdleTimer(t *testing.T) {
	c, now, closed := newTestCache(time.Minute)
	defer c.Dispose()

	c.Store("uid-abc", sqlds.CachedConnection{})
	*now = now.Add(45 * time.Second)
	_, ok := c.Load("uid-abc")
	require.True(t, ok)
	*now = now.Add(45 * time.Second)
	c.sweep()

	_, ok = c.Load("uid-abc")
	assert.True(t, ok, "entry used within the TTL must survive")
	assert.Empty(t, *closed)
}

func TestIdleTTLCacheNeverEvictsDefaultEntry(t *testing.T) {
	c, now, closed := newTestCache(time.Minute)
	defer c.Dispose()

	c.Store("uid-default", sqlds.CachedConnection{})
	*now = now.Add(time.Hour)
	c.sweep()

	_, ok := c.Load("uid-default")
	assert.True(t, ok, "sqlds requires the default entry to stay present")
	assert.Empty(t, *closed)
}

func TestIdleTTLCacheDisposeClosesAllEntries(t *testing.T) {
	c, _, closed := newTestCache(time.Minute)
	c.Store("uid-default", sqlds.CachedConnection{})
	c.Store("uid-abc", sqlds.CachedConnection{})

	c.Dispose()

	assert.ElementsMatch(t, []string{"uid-default", "uid-abc"}, *closed)
	_, ok := c.Load("uid-abc")
	assert.False(t, ok)
	c.Dispose() // idempotent
}

func TestIdleTTLCacheRange(t *testing.T) {
	c, _, _ := newTestCache(time.Minute)
	defer c.Dispose()
	c.Store("a", sqlds.CachedConnection{})
	c.Store("b", sqlds.CachedConnection{})

	keys := map[string]bool{}
	c.Range(func(k string, _ sqlds.CachedConnection) bool { keys[k] = true; return true })
	assert.Equal(t, map[string]bool{"a": true, "b": true}, keys)
}

func TestIdleTTLCacheSweepGoroutineEvicts(t *testing.T) {
	c := newIdleTTLCache(10 * time.Millisecond)
	defer c.Dispose()
	c.Store("uid-abc", sqlds.CachedConnection{})

	assert.Eventually(t, func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		_, ok := c.entries["uid-abc"]
		return !ok
	}, 2*time.Second, 5*time.Millisecond)
}

func TestIdleTTLCacheRetiredEntryIsClosedAfterTTL(t *testing.T) {
	c, now, closed := newTestCache(time.Minute)
	defer c.Dispose()

	// sqlds has no constructor for a CachedConnection holding a real
	// *sql.DB, so a pool overwritten under the same key is simulated by
	// retiring it directly.
	c.retired = append(c.retired, idleTTLRetired{key: "uid-abc", retiredAt: *now})
	c.sweep()
	assert.Empty(t, *closed, "the loser of a race may still be in use")

	*now = now.Add(2 * time.Minute)
	c.sweep()
	assert.Equal(t, []string{"uid-abc"}, *closed)
}

func TestIdleTTLCacheStoreOfSamePoolDoesNotRetireIt(t *testing.T) {
	c, now, closed := newTestCache(time.Minute)
	defer c.Dispose()

	c.Store("uid-abc", sqlds.CachedConnection{})
	c.Store("uid-abc", sqlds.CachedConnection{})
	*now = now.Add(2 * time.Minute)
	c.Load("uid-abc")
	c.sweep()

	assert.Empty(t, *closed)
}

func TestNewDatasourceInstallsEvictingCacheOnlyWhenForwardingHeaders(t *testing.T) {
	for name, tc := range map[string]struct {
		jsonData string
		want     bool
	}{
		"forwarding":     {`{"forwardGrafanaHeaders":true}`, true},
		"not forwarding": {`{}`, false},
	} {
		t.Run(name, func(t *testing.T) {
			inst, err := NewDatasource(context.Background(), backend.DataSourceInstanceSettings{JSONData: []byte(tc.jsonData)})
			require.NoError(t, err)
			ds, ok := inst.(*sqlds.SQLDatasource)
			require.True(t, ok)
			assert.Equal(t, tc.want, ds.ConnectionCacheFactory != nil)
			ds.Dispose()
		})
	}
}
