package cacherefresh

import (
	"cmp"
	"context"
	"hash/fnv"
	"slices"
	"time"

	"github.com/AdguardTeam/golibs/errors"
	"github.com/AdguardTeam/golibs/logutil/slogutil"
)

// candidate is an entry that may be refreshed during a tick.
type candidate struct {
	entry *entry
	hits  uint64
}

// tick starts the refreshes of the entries that are due at now, at most as
// many as there are free refresh slots, the most queried first.
func (s *Service) tick(ctx context.Context, now time.Time) {
	free := cap(s.sema) - len(s.sema)
	if free <= 0 {
		return
	}

	s.tracker.mu.Lock()
	defer s.tracker.mu.Unlock()

	candidates := s.candidatesLocked(now)
	for i, c := range candidates {
		if free == 0 {
			break
		}

		e := c.entry
		minRemaining, ok := s.dueParams(e, i, now)
		if !ok {
			continue
		}

		select {
		case s.sema <- struct{}{}:
		default:
			// All slots are taken by refreshes started by the previous ticks
			// that have finished after free was calculated.
			return
		}

		free--
		e.inFlight = true

		s.wg.Add(1)
		go s.refresh(ctx, e, minRemaining)
	}
}

// dueParams returns the minimum remaining validity to request from the
// [Refresher] and true if e, which is at position i among the candidates, is
// due to be refreshed at now.  s.tracker.mu must be locked.
func (s *Service) dueParams(e *entry, i int, now time.Time) (minRemaining time.Duration, ok bool) {
	if e.inFlight || e.failures >= maxFailures || now.Before(e.nextTry) {
		return 0, false
	}

	due := e.expiry.Add(s.delay)
	if uint64(i) < uint64(s.prefetchTop) {
		// Never prefetch earlier than halfway through the validity.
		minRemaining = min(s.prefetchBefore, e.ttl/2)
		due = e.expiry.Add(-minRemaining)
	}

	return minRemaining, !now.Before(due)
}

// candidatesLocked returns the tracked entries that have more queries than
// required, sorted by the number of queries in descending order.  It also
// removes the entries that are no longer worth tracking.  s.tracker.mu must be
// locked.
func (s *Service) candidatesLocked(now time.Time) (candidates []candidate) {
	t := s.tracker
	for _, e := range t.entries {
		hits := e.hits(now)
		switch {
		case hits == 0:
			// No queries within the window.
			t.removeLocked(e)
		case e.expiry.IsZero():
			// Not cached.
		case now.After(e.expiry.Add(s.optimisticMaxAge)):
			// Removed from the cache by dnsproxy.  The next client query
			// tracks it again.
			t.removeLocked(e)
		case hits > uint64(s.minHits):
			candidates = append(candidates, candidate{entry: e, hits: hits})
		default:
			// Not popular enough.
		}
	}

	slices.SortFunc(candidates, func(a, b candidate) (res int) {
		if res = cmp.Compare(b.hits, a.hits); res != 0 {
			return res
		}

		return a.entry.expiry.Compare(b.entry.expiry)
	})

	return candidates
}

// refresh refreshes e and updates its state.  It's intended to be used as a
// goroutine.
func (s *Service) refresh(ctx context.Context, e *entry, minRemaining time.Duration) {
	defer s.wg.Done()
	defer func() { <-s.sema }()
	defer slogutil.RecoverAndLog(ctx, s.logger)

	ctx, cancel := context.WithTimeout(ctx, s.refreshTimeout)
	defer cancel()

	ttl, refreshed, err := s.refresher.Refresh(ctx, e.key, minRemaining)
	now := s.clock.Now()

	s.tracker.mu.Lock()
	defer s.tracker.mu.Unlock()

	e.inFlight = false
	tracked := s.tracker.isTrackedLocked(e)

	switch {
	case errors.Is(err, ErrInProgress):
		s.skipped.Add(1)
		e.nextTry = now.Add(inProgressRetry)
	case err != nil:
		s.failed.Add(1)
		e.failures++
		e.nextTry = now.Add(backoff(e.failures) + jitter(e.key))
		s.logger.DebugContext(
			ctx,
			"refreshing",
			"name", e.key.Name,
			"qtype", e.key.Qtype,
			"failures", e.failures,
			slogutil.KeyError, err,
		)
	case refreshed && ttl <= 0:
		// The response isn't cacheable anymore.
		s.skipped.Add(1)
		if tracked {
			s.tracker.removeLocked(e)
		}
	case refreshed:
		s.refreshed.Add(1)
		e.failures = 0
		e.nextTry = time.Time{}
		e.expiry = now.Add(ttl)
		e.ttl = ttl
		s.logger.DebugContext(ctx, "refreshed", "name", e.key.Name, "qtype", e.key.Qtype, "ttl", ttl)
	default:
		// Still fresh, probably refreshed by a client query.  dnsproxy keeps
		// expiry times in whole seconds, so ttl may be zero for an item that
		// is about to expire.
		s.skipped.Add(1)
		e.expiry = now.Add(max(ttl, time.Second))
	}
}

// backoff returns the delay before the next attempt after the given number of
// consecutive failures, which must be positive.
func backoff(failures uint8) (d time.Duration) {
	if shift := failures - 1; shift < 16 {
		return min(backoffBase<<shift, backoffMax)
	}

	return backoffMax
}

// jitter returns a delay below backoffBase that is stable for k, so that the
// keys that failed together don't retry together.
func jitter(k Key) (d time.Duration) {
	h := fnv.New64a()
	_, _ = h.Write([]byte(k.Name))
	_, _ = h.Write([]byte{byte(k.Qtype >> 8), byte(k.Qtype)})

	return time.Duration(h.Sum64() % uint64(backoffBase))
}
