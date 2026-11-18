package cacherefresh

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/AdguardTeam/golibs/errors"
	"github.com/AdguardTeam/golibs/logutil/slogutil"
	"github.com/AdguardTeam/golibs/testutil"
	"github.com/AdguardTeam/golibs/testutil/faketime"
	"github.com/miekg/dns"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testTimeout is the common timeout for tests.
const testTimeout = 1 * time.Second

// testStart is the initial time of the test clock.
var testStart = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// testClock is a manually advanced clock for tests.
type testClock struct {
	mu  *sync.Mutex
	now time.Time
}

// newTestClock returns a new clock set to testStart.
func newTestClock() (c *testClock) {
	return &testClock{mu: &sync.Mutex{}, now: testStart}
}

// clock returns c as a [faketime.Clock].
func (c *testClock) clock() (fc *faketime.Clock) {
	return &faketime.Clock{OnNow: c.get}
}

// get returns the current time of c.
func (c *testClock) get() (now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}

// advance moves c forward by d and returns the new time.
func (c *testClock) advance(d time.Duration) (now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.now = c.now.Add(d)

	return c.now
}

// refreshCall is a recorded call of [testRefresher.Refresh].
type refreshCall struct {
	key          Key
	minRemaining time.Duration
}

// testRefresher is a [Refresher] for tests.
type testRefresher struct {
	onRefresh func(k Key, minRemaining time.Duration) (ttl time.Duration, ok bool, err error)

	mu    *sync.Mutex
	calls []refreshCall
}

// newTestRefresher returns a refresher that calls onRefresh.
func newTestRefresher(
	onRefresh func(k Key, minRemaining time.Duration) (ttl time.Duration, ok bool, err error),
) (r *testRefresher) {
	return &testRefresher{onRefresh: onRefresh, mu: &sync.Mutex{}}
}

// type check
var _ Refresher = (*testRefresher)(nil)

// Refresh implements the [Refresher] interface for *testRefresher.
func (r *testRefresher) Refresh(
	_ context.Context,
	k Key,
	minRemaining time.Duration,
) (ttl time.Duration, refreshed bool, err error) {
	r.mu.Lock()
	r.calls = append(r.calls, refreshCall{key: k, minRemaining: minRemaining})
	r.mu.Unlock()

	return r.onRefresh(k, minRemaining)
}

// takeCalls returns the recorded calls and forgets them.
func (r *testRefresher) takeCalls() (calls []refreshCall) {
	r.mu.Lock()
	defer r.mu.Unlock()

	calls, r.calls = r.calls, nil

	return calls
}

// newTestService returns a service that isn't started, so that tests drive it
// with [Service.tick].
func newTestService(tb testing.TB, c *Config, clock *testClock, r Refresher) (s *Service) {
	tb.Helper()

	conf := &Config{
		Logger:           slogutil.NewDiscardLogger(),
		Clock:            clock.clock(),
		Refresher:        r,
		Delay:            time.Second,
		PrefetchBefore:   10 * time.Second,
		OptimisticMaxAge: 12 * time.Hour,
		RefreshTimeout:   testTimeout,
		MaxConcurrent:    4,
		MinHits:          0,
		PrefetchTop:      0,
		MaxTracked:       100,
	}

	if c != nil {
		conf.Delay = c.Delay
		conf.PrefetchBefore = c.PrefetchBefore
		conf.MinHits = c.MinHits
		conf.PrefetchTop = c.PrefetchTop

		if c.MaxConcurrent > 0 {
			conf.MaxConcurrent = c.MaxConcurrent
		}

		if c.MaxTracked > 0 {
			conf.MaxTracked = c.MaxTracked
		}
	}

	return New(conf)
}

// tickAndWait runs a tick at now and waits for the started refreshes.
func tickAndWait(s *Service, now time.Time) {
	s.tick(context.Background(), now)
	s.wg.Wait()
}

// newKey returns an A key for name.
func newKey(name string) (k Key) {
	return Key{Name: name, Qtype: dns.TypeA, Qclass: dns.ClassINET}
}

// okRefresh returns a refresh function that succeeds with the given TTL.
func okRefresh(ttl time.Duration) func(Key, time.Duration) (time.Duration, bool, error) {
	return func(_ Key, _ time.Duration) (time.Duration, bool, error) {
		return ttl, true, nil
	}
}

func TestKeyFromMsg(t *testing.T) {
	req := (&dns.Msg{}).SetQuestion("ExAmple.ORG.", dns.TypeAAAA)
	req.SetEdns0(udpBufSize, true)

	k, ok := KeyFromMsg(req)
	require.True(t, ok)

	assert.Equal(t, Key{
		Name:   "example.org.",
		Qtype:  dns.TypeAAAA,
		Qclass: dns.ClassINET,
		DO:     true,
	}, k)

	got, ok := KeyFromMsg(k.NewRequest())
	require.True(t, ok)
	assert.Equal(t, k, got)

	k.DO = false
	newReq := k.NewRequest()
	assert.Nil(t, newReq.IsEdns0())
	assert.True(t, newReq.RecursionDesired)

	_, ok = KeyFromMsg(&dns.Msg{})
	assert.False(t, ok)

	_, ok = KeyFromMsg(nil)
	assert.False(t, ok)
}

func TestResponseTTL(t *testing.T) {
	newA := func(ttl uint32) (rr dns.RR) {
		return &dns.A{
			Hdr: dns.RR_Header{Name: "a.", Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: ttl},
			A:   net.IP{1, 2, 3, 4},
		}
	}
	newSOA := func(ttl uint32) (rr dns.RR) {
		return &dns.SOA{
			Hdr: dns.RR_Header{Name: "a.", Rrtype: dns.TypeSOA, Class: dns.ClassINET, Ttl: ttl},
		}
	}
	newCNAME := func(ttl uint32) (rr dns.RR) {
		return &dns.CNAME{
			Hdr:    dns.RR_Header{Name: "a.", Rrtype: dns.TypeCNAME, Class: dns.ClassINET, Ttl: ttl},
			Target: "b.",
		}
	}
	newNS := func(ttl uint32) (rr dns.RR) {
		return &dns.NS{
			Hdr: dns.RR_Header{Name: "a.", Rrtype: dns.TypeNS, Class: dns.ClassINET, Ttl: ttl},
			Ns:  "ns.a.",
		}
	}
	newResp := func(rcode int, ans, ns []dns.RR) (m *dns.Msg) {
		req := (&dns.Msg{}).SetQuestion("a.", dns.TypeA)
		m = (&dns.Msg{}).SetRcode(req, rcode)
		m.Answer, m.Ns = ans, ns
		m.SetEdns0(udpBufSize, true)

		return m
	}

	truncated := newResp(dns.RcodeSuccess, []dns.RR{newA(10)}, nil)
	truncated.Truncated = true

	testCases := []struct {
		resp *dns.Msg
		name string
		want time.Duration
	}{{
		resp: newResp(dns.RcodeSuccess, []dns.RR{newA(100), newA(60)}, nil),
		name: "min_answer",
		want: 60 * time.Second,
	}, {
		resp: newResp(dns.RcodeNameError, nil, []dns.RR{newSOA(300)}),
		name: "nxdomain_soa",
		want: 300 * time.Second,
	}, {
		resp: newResp(dns.RcodeServerFailure, nil, []dns.RR{newSOA(300)}),
		name: "servfail_capped",
		want: servFailMaxTTL,
	}, {
		resp: newResp(dns.RcodeRefused, []dns.RR{newA(10)}, nil),
		name: "refused",
		want: 0,
	}, {
		resp: newResp(dns.RcodeSuccess, nil, nil),
		name: "only_opt",
		want: 0,
	}, {
		resp: truncated,
		name: "truncated_ignored",
		want: 10 * time.Second,
	}, {
		resp: newResp(dns.RcodeSuccess, []dns.RR{newCNAME(10)}, nil),
		name: "dangling_cname",
		want: 0,
	}, {
		resp: newResp(dns.RcodeNameError, nil, []dns.RR{newSOA(300), newNS(300)}),
		name: "nxdomain_referral",
		want: 0,
	}, {
		resp: nil,
		name: "nil",
		want: 0,
	}}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, ResponseTTL(tc.resp))
		})
	}
}

func TestTracker_hits(t *testing.T) {
	tr := newTracker(2)
	k := newKey("a.")

	tr.hit(k, 0, false, testStart)
	assert.Zero(t, tr.len(), "uncached keys aren't tracked")

	tr.hit(k, time.Minute, false, testStart)
	tr.hit(k, time.Minute, false, testStart.Add(time.Hour))

	e := tr.entries[k]
	require.NotNil(t, e)

	assert.Equal(t, uint64(2), e.hits(testStart.Add(time.Hour)))
	assert.Equal(t, uint64(2), e.hits(testStart.Add(24*time.Hour+59*time.Minute)))
	assert.Equal(t, uint64(1), e.hits(testStart.Add(25*time.Hour)))
	assert.Zero(t, e.hits(testStart.Add(26*time.Hour)))

	tr.hit(newKey("b."), time.Minute, false, testStart)
	tr.hit(k, time.Minute, false, testStart)
	tr.hit(newKey("c."), time.Minute, false, testStart)

	assert.Equal(t, 2, tr.len())
	assert.Contains(t, tr.entries, k)
	assert.NotContains(t, tr.entries, newKey("b."), "least recently queried is evicted")

	tr.clear()
	assert.Zero(t, tr.len())
}

func TestService_delay(t *testing.T) {
	clock := newTestClock()
	r := newTestRefresher(okRefresh(time.Minute))
	s := newTestService(t, &Config{Delay: 5 * time.Second, MinHits: 1}, clock, r)

	hot, cold := newKey("hot."), newKey("cold.")
	s.Hit(hot, time.Minute, false)
	s.Hit(hot, time.Minute, false)
	s.Hit(cold, time.Minute, false)

	tickAndWait(s, clock.advance(time.Minute))
	assert.Empty(t, r.takeCalls(), "not due before expiry plus delay")

	tickAndWait(s, clock.advance(5*time.Second))
	assert.Equal(t, []refreshCall{{key: hot, minRemaining: 0}}, r.takeCalls())

	tickAndWait(s, clock.advance(time.Minute))
	assert.Empty(t, r.takeCalls(), "expiry is updated from the refresh")

	tickAndWait(s, clock.advance(5*time.Second))
	assert.Len(t, r.takeCalls(), 1)

	c := s.Counters()
	assert.Equal(t, uint64(2), c.Refreshed)
	assert.Equal(t, uint64(2), c.Tracked)
}

func TestService_prefetch(t *testing.T) {
	clock := newTestClock()
	r := newTestRefresher(okRefresh(time.Minute))
	s := newTestService(t, &Config{
		Delay:          time.Second,
		PrefetchBefore: 10 * time.Second,
		PrefetchTop:    1,
	}, clock, r)

	top, other := newKey("top."), newKey("other.")
	s.Hit(top, time.Minute, false)
	s.Hit(top, time.Minute, false)
	s.Hit(other, time.Minute, false)

	tickAndWait(s, clock.advance(50*time.Second))
	assert.Equal(t, []refreshCall{{key: top, minRemaining: 10 * time.Second}}, r.takeCalls())

	tickAndWait(s, clock.advance(11*time.Second))
	assert.Equal(t, []refreshCall{{key: other, minRemaining: 0}}, r.takeCalls())

	t.Run("short_ttl", func(t *testing.T) {
		short := newKey("short.")
		for range 5 {
			s.Hit(short, 4*time.Second, false)
		}

		// The prefetch window is limited to half of the TTL.
		tickAndWait(s, clock.advance(time.Second))
		assert.Empty(t, r.takeCalls())

		tickAndWait(s, clock.advance(time.Second))
		assert.Equal(t, []refreshCall{{key: short, minRemaining: 2 * time.Second}}, r.takeCalls())
	})
}

func TestService_concurrency(t *testing.T) {
	clock := newTestClock()

	release := make(chan struct{})
	started := make(chan Key, 10)
	r := newTestRefresher(func(k Key, _ time.Duration) (time.Duration, bool, error) {
		started <- k
		<-release

		return time.Minute, true, nil
	})
	s := newTestService(t, &Config{MaxConcurrent: 2, Delay: 0}, clock, r)

	keys := []Key{newKey("a."), newKey("b."), newKey("c."), newKey("d.")}
	for i, k := range keys {
		// The first key is the most queried one.
		for range len(keys) - i {
			s.Hit(k, time.Minute, false)
		}
	}

	now := clock.advance(time.Minute)
	ctx := context.Background()
	s.tick(ctx, now)

	first, _ := testutil.RequireReceive(t, started, testTimeout)
	second, _ := testutil.RequireReceive(t, started, testTimeout)
	got := []Key{first, second}
	assert.ElementsMatch(t, keys[:2], got, "the most queried first")

	s.tick(ctx, now)
	assert.Empty(t, started, "no free slots")

	close(release)
	s.wg.Wait()
	r.takeCalls()

	tickAndWait(s, now)
	calls := r.takeCalls()
	require.Len(t, calls, 2)
	assert.ElementsMatch(t, keys[2:], []Key{calls[0].key, calls[1].key})
}

func TestService_backoff(t *testing.T) {
	clock := newTestClock()

	testErr := errors.Error("upstream failed")
	r := newTestRefresher(func(_ Key, _ time.Duration) (time.Duration, bool, error) {
		return 0, false, testErr
	})
	s := newTestService(t, &Config{Delay: 0}, clock, r)

	k := newKey("a.")
	s.Hit(k, time.Minute, false)

	tickAndWait(s, clock.advance(time.Minute))
	require.Len(t, r.takeCalls(), 1)

	tickAndWait(s, clock.advance(time.Second))
	assert.Empty(t, r.takeCalls(), "backoff")

	// A client query doesn't reset the backoff of a retrying key.
	s.Hit(k, 0, false)
	tickAndWait(s, clock.advance(time.Second))
	assert.Empty(t, r.takeCalls())

	for i := range maxFailures - 1 {
		tickAndWait(s, clock.advance(backoffMax+backoffBase))
		assert.Len(t, r.takeCalls(), 1, "attempt %d", i+2)
	}

	tickAndWait(s, clock.advance(backoffMax+backoffBase))
	assert.Empty(t, r.takeCalls(), "parked after max failures")
	assert.Equal(t, uint64(maxFailures), s.Counters().Failed)

	s.Hit(k, 0, false)
	tickAndWait(s, clock.advance(time.Second))
	assert.Len(t, r.takeCalls(), 1, "re-armed by a client query")
}

func TestBackoff(t *testing.T) {
	for n, want := range map[uint8]time.Duration{
		1:   backoffBase,
		2:   2 * backoffBase,
		3:   4 * backoffBase,
		10:  backoffMax,
		255: backoffMax,
	} {
		assert.Equal(t, want, backoff(n), "failures %d", n)
	}

	a, b := newKey("a."), newKey("b.")
	assert.Equal(t, jitter(a), jitter(a))
	assert.NotEqual(t, jitter(a), jitter(b))
	assert.Less(t, jitter(a), backoffBase)
}

func TestService_results(t *testing.T) {
	clock := newTestClock()

	var (
		ttl       time.Duration
		refreshed bool
		err       error
	)
	r := newTestRefresher(func(_ Key, _ time.Duration) (time.Duration, bool, error) {
		return ttl, refreshed, err
	})
	s := newTestService(t, &Config{Delay: 0}, clock, r)

	k := newKey("a.")
	s.Hit(k, time.Minute, false)

	// In progress elsewhere.
	ttl, refreshed, err = 0, false, ErrInProgress
	tickAndWait(s, clock.advance(time.Minute))
	tickAndWait(s, clock.advance(time.Second))
	assert.Len(t, r.takeCalls(), 1)

	tickAndWait(s, clock.advance(inProgressRetry))
	assert.Len(t, r.takeCalls(), 1, "retried")

	// Still fresh.
	ttl, refreshed, err = 30*time.Second, false, nil
	tickAndWait(s, clock.advance(inProgressRetry))
	assert.Len(t, r.takeCalls(), 1)

	tickAndWait(s, clock.advance(29*time.Second))
	assert.Empty(t, r.takeCalls(), "expiry updated from remaining ttl")

	// Not cacheable anymore.
	ttl, refreshed, err = 0, true, nil
	tickAndWait(s, clock.advance(time.Second))
	assert.Len(t, r.takeCalls(), 1)

	c := s.Counters()
	assert.Equal(t, uint64(4), c.Skipped)
	assert.Zero(t, c.Tracked)
}

func TestService_expiredTooLong(t *testing.T) {
	clock := newTestClock()
	r := newTestRefresher(okRefresh(time.Minute))
	s := newTestService(t, &Config{Delay: 13 * time.Hour}, clock, r)

	s.Hit(newKey("a."), time.Minute, false)

	tickAndWait(s, clock.advance(12*time.Hour+2*time.Minute))
	assert.Empty(t, r.takeCalls())
	assert.Zero(t, s.Counters().Tracked)
}

func TestService_StartShutdown(t *testing.T) {
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })

	started := make(chan struct{}, 1)
	r := newTestRefresher(func(_ Key, _ time.Duration) (time.Duration, bool, error) {
		select {
		case started <- struct{}{}:
		default:
		}

		<-block

		return 0, false, context.Canceled
	})

	s := New(&Config{
		Logger:           slogutil.NewDiscardLogger(),
		Refresher:        r,
		Delay:            0,
		OptimisticMaxAge: time.Hour,
		RefreshTimeout:   time.Hour,
		TickInterval:     10 * time.Millisecond,
		MaxConcurrent:    1,
		MaxTracked:       10,
	})

	s.Hit(newKey("a."), time.Millisecond, false)

	require.NoError(t, s.Start(context.Background()))
	testutil.RequireReceive(t, started, testTimeout)

	// Shutdown doesn't wait for the refresh, which ignores cancellation.
	ctx := testutil.ContextWithTimeout(t, testTimeout)
	assert.NoError(t, s.Shutdown(ctx))
	assert.NoError(t, s.Shutdown(ctx))

	// Shutting down a service that isn't started is a no-op.
	assert.NoError(t, New(&Config{MaxConcurrent: 1, MaxTracked: 1}).Shutdown(ctx))
}

func TestService_panic(t *testing.T) {
	clock := newTestClock()
	r := newTestRefresher(func(_ Key, _ time.Duration) (time.Duration, bool, error) {
		panic("test panic")
	})
	s := newTestService(t, &Config{Delay: 0}, clock, r)

	k := newKey("a.")
	s.Hit(k, time.Minute, false)

	tickAndWait(s, clock.advance(time.Minute))
	assert.Len(t, r.takeCalls(), 1)
	assert.Equal(t, uint64(1), s.Counters().Failed)

	// The entry isn't stuck in flight.
	tickAndWait(s, clock.advance(backoffMax+backoffBase))
	assert.Len(t, r.takeCalls(), 1)
}

func TestService_overdueFirst(t *testing.T) {
	clock := newTestClock()
	r := newTestRefresher(okRefresh(2 * time.Second))
	s := newTestService(t, &Config{MaxConcurrent: 1, Delay: 0, PrefetchTop: 1}, clock, r)

	hot, cold := newKey("hot."), newKey("cold.")
	for range 10 {
		s.Hit(hot, 2*time.Second, false)
	}
	s.Hit(cold, 500*time.Millisecond, false)

	// The cold entry is due first, so it wins the only slot even though the
	// hot one is due too.
	tickAndWait(s, clock.advance(2*time.Second))
	assert.Equal(t, []refreshCall{{key: cold, minRemaining: 0}}, r.takeCalls())
}

func TestTracker_cachedExpiry(t *testing.T) {
	tr := newTracker(10)
	k := newKey("a.")

	// The validity of a cached response for an unknown key is unknown.
	tr.hit(k, 30*time.Second, true, testStart)
	e := tr.entries[k]
	require.NotNil(t, e)

	assert.Equal(t, testStart, e.expiry)
	assert.Zero(t, e.ttl)

	// A response from an upstream sets the expiry.
	tr.hit(k, time.Minute, false, testStart)
	assert.Equal(t, testStart.Add(time.Minute), e.expiry)
	assert.Equal(t, time.Minute, e.ttl)

	// An expired response served optimistically doesn't move the expiry later.
	tr.hit(k, 30*time.Second, true, testStart.Add(70*time.Second))
	assert.Equal(t, testStart.Add(time.Minute), e.expiry)
	assert.Equal(t, time.Minute, e.ttl)

	// A cached response may move it earlier.
	tr.hit(k, 10*time.Second, true, testStart.Add(40*time.Second))
	assert.Equal(t, testStart.Add(50*time.Second), e.expiry)
}
