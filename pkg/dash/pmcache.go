package dash

import (
	"sync"
	"time"
)

// TODO we can do better.  don't allow multiple lookups to proceed at the same time.

type pmCacheEntry struct {
	Ts       time.Time
	Mappings map[string]*Control
}

type panelMappingsCache struct {
	Lock  *sync.Mutex
	Cache map[string]pmCacheEntry
}

var pmCache = panelMappingsCache{
	Lock:  &sync.Mutex{},
	Cache: make(map[string]pmCacheEntry),
}

func getMappingsFromCache(panelName string, exp time.Duration) (map[string]*Control, bool) {
	if exp <= time.Millisecond {
		return nil, false
	}

	now := time.Now()
	pmCache.Lock.Lock()
	defer pmCache.Lock.Unlock()

	entry, ok := pmCache.Cache[panelName]
	if !ok {
		return nil, false
	}
	if entry.Ts.Add(exp).Before(now) {
		// entry expired, return false
		// we don't clear it because someone else could ask with a different exp duration
		return nil, false
	}
	return entry.Mappings, true
}

func setMappingsInCache(panelName string, mappings map[string]*Control) {
	pmCache.Lock.Lock()
	defer pmCache.Lock.Unlock()

	pmCache.Cache[panelName] = pmCacheEntry{Ts: time.Now(), Mappings: mappings}
}
