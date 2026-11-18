package cacherefresh

import (
	"container/list"
	"sync"
	"time"
)

// entry is the tracking state of a single cache key.
type entry struct {
	// expiry is the time when the cached response expires.  It's zero until a
	// cacheable response has been seen.
	expiry time.Time

	// nextTry is the earliest time of the next refresh attempt.
	nextTry time.Time

	// elem is the position of the entry in the LRU list of the tracker.
	elem *list.Element

	key Key

	// ttl is the validity of the last response received from an upstream.  It
	// bounds the prefetch window, so that entries with short TTLs aren't
	// refreshed continuously.  It's zero until such a response has been seen.
	ttl time.Duration

	// hourIdx is the number of hours since the Unix epoch that corresponds to
	// buckets[cur].
	hourIdx int64

	// cur is the index of the bucket of the current hour.
	cur int

	// buckets contain the numbers of queries for each of the last hours.
	buckets [hitWindowHours]uint32

	// failures is the number of consecutive failed refreshes.
	failures uint8

	// inFlight is true while the entry is being refreshed.
	inFlight bool
}

// roll advances the hourly buckets of e to now, clearing the buckets of the
// hours without queries.
func (e *entry) roll(now time.Time) {
	hour := now.Unix() / int64(time.Hour/time.Second)
	delta := hour - e.hourIdx
	if delta <= 0 {
		return
	}

	e.hourIdx = hour
	if delta >= hitWindowHours {
		e.buckets = [hitWindowHours]uint32{}
		e.cur = 0

		return
	}

	for range delta {
		e.cur = (e.cur + 1) % hitWindowHours
		e.buckets[e.cur] = 0
	}
}

// hits returns the number of queries for e over the last 24 hours.
func (e *entry) hits(now time.Time) (n uint64) {
	e.roll(now)
	for _, b := range e.buckets {
		n += uint64(b)
	}

	return n
}

// tracker is a bounded set of tracked cache keys with LRU eviction.
type tracker struct {
	// mu protects all fields below, as well as the fields of the entries.
	mu *sync.Mutex

	entries map[Key]*entry

	// lru contains *entry values, the most recently queried at the front.
	lru *list.List

	maxLen uint32
}

// newTracker returns a new tracker that keeps at most maxLen keys.
func newTracker(maxLen uint32) (t *tracker) {
	return &tracker{
		mu:      &sync.Mutex{},
		entries: map[Key]*entry{},
		lru:     list.New(),
		maxLen:  maxLen,
	}
}

// hit records a client query for k at now.  ttl is the validity of the
// response; zero means that the response isn't cached.  cached is true if the
// response came from the cache.
func (t *tracker) hit(k Key, ttl time.Duration, cached bool, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()

	e, ok := t.entries[k]
	if !ok {
		if ttl <= 0 {
			// Don't track keys that aren't cached.
			return
		}

		e = &entry{
			key:     k,
			hourIdx: now.Unix() / int64(time.Hour/time.Second),
		}
		e.elem = t.lru.PushFront(e)
		t.entries[k] = e

		for uint32(len(t.entries)) > t.maxLen {
			t.removeLocked(t.lru.Back().Value.(*entry))
		}
	} else {
		t.lru.MoveToFront(e.elem)
	}

	e.roll(now)
	e.buckets[e.cur]++

	if ttl > 0 {
		e.updateExpiry(ttl, cached, now)
	}

	// A client query re-arms the refreshing of a key that failed too often.
	// Keys that are still retrying keep their backoff, so that the queries for
	// a popular key don't defeat it during an upstream outage.
	if e.failures >= maxFailures {
		e.failures = 0
		e.nextTry = time.Time{}
	}
}

// updateExpiry updates the expiry of e from a client response with the given
// positive ttl received at now.
func (e *entry) updateExpiry(ttl time.Duration, cached bool, now time.Time) {
	switch {
	case !cached:
		e.expiry = now.Add(ttl)
		e.ttl = ttl
	case e.expiry.IsZero():
		// The cached response may be an expired one, so the expiry is
		// unknown.  Consider the entry expired, so that the next refresh
		// attempt either refreshes it or learns the remaining validity.
		e.expiry = now
	default:
		// An expired response is served with a fixed TTL that is unrelated to
		// the actual expiry, so never move the expiry later.
		if exp := now.Add(ttl); exp.Before(e.expiry) {
			e.expiry = exp
		}
	}
}

// removeLocked stops tracking e.  t.mu must be locked.
func (t *tracker) removeLocked(e *entry) {
	t.lru.Remove(e.elem)
	delete(t.entries, e.key)
}

// isTrackedLocked returns true if e is still tracked.  t.mu must be locked.
func (t *tracker) isTrackedLocked(e *entry) (ok bool) {
	return t.entries[e.key] == e
}

// clear stops tracking all keys.
func (t *tracker) clear() {
	t.mu.Lock()
	defer t.mu.Unlock()

	clear(t.entries)
	t.lru.Init()
}

// len returns the number of tracked keys.
func (t *tracker) len() (n int) {
	t.mu.Lock()
	defer t.mu.Unlock()

	return len(t.entries)
}
