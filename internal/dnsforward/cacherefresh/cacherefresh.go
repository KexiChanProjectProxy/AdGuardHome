// Package cacherefresh implements proactive refreshing of popular entries of
// the optimistic DNS cache.
//
// The cache itself lives in dnsproxy and can't be iterated, so the service
// tracks the cache keys of resolved client queries itself, counts the queries
// for each key over the last 24 hours, and asks a [Refresher] to re-resolve the
// popular keys around their expiry.
package cacherefresh

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/AdguardTeam/golibs/errors"
	"github.com/AdguardTeam/golibs/logutil/slogutil"
	"github.com/AdguardTeam/golibs/timeutil"
	"github.com/miekg/dns"
)

// Key identifies an entry of the general dnsproxy cache.  It mirrors the cache
// key of dnsproxy: the DO bit, QTYPE, QCLASS, and the lowercased QNAME.
type Key struct {
	// Name is the lowercased fully-qualified domain name of the question.
	Name string

	// Qtype is the type of the question.
	Qtype uint16

	// Qclass is the class of the question.
	Qclass uint16

	// DO is the DNSSEC OK bit of the request.
	DO bool
}

// KeyFromMsg returns the cache key for req.  ok is false if req is nil or
// doesn't have exactly one question.
func KeyFromMsg(req *dns.Msg) (k Key, ok bool) {
	if req == nil || len(req.Question) != 1 {
		return Key{}, false
	}

	q := req.Question[0]
	opt := req.IsEdns0()

	return Key{
		Name:   strings.ToLower(q.Name),
		Qtype:  q.Qtype,
		Qclass: q.Qclass,
		DO:     opt != nil && opt.Do(),
	}, true
}

// udpBufSize is the EDNS0 UDP buffer size of refresh requests with the DO bit.
const udpBufSize = 2048

// NewRequest returns a new recursive request message with the question and the
// DO bit of k.
func (k Key) NewRequest() (req *dns.Msg) {
	req = (&dns.Msg{}).SetQuestion(k.Name, k.Qtype)
	req.Question[0].Qclass = k.Qclass
	if k.DO {
		req.SetEdns0(udpBufSize, true)
	}

	return req
}

// servFailMaxTTL is the maximum TTL of cached SERVFAIL responses in dnsproxy.
const servFailMaxTTL = 30 * time.Second

// ResponseTTL returns the duration for which resp is valid, as seen by a
// client.  It mirrors the way dnsproxy calculates the TTL of cached items and
// returns zero if dnsproxy wouldn't cache resp.  resp is expected to be the
// response to a request with a single question.
//
// The client response may differ from the cached one: DNSSEC records may be
// removed and the message may be truncated for the client.  So the truncation
// is ignored, and the result is only a hint that is corrected by refreshes.
func ResponseTTL(resp *dns.Msg) (ttl time.Duration) {
	if resp == nil || len(resp.Question) != 1 || !isCacheable(resp) {
		return 0
	}

	minTTL, ok := minRRTTL(resp)
	if !ok {
		return 0
	}

	ttl = time.Duration(minTTL) * time.Second
	if resp.Rcode == dns.RcodeServerFailure {
		ttl = min(ttl, servFailMaxTTL)
	}

	return ttl
}

// isCacheable returns true if dnsproxy caches resp, judging by its response
// code and contents.  resp must have exactly one question.
func isCacheable(resp *dns.Msg) (ok bool) {
	switch resp.Rcode {
	case dns.RcodeSuccess:
		qt := resp.Question[0].Qtype

		return (qt != dns.TypeA && qt != dns.TypeAAAA) || hasIPAnswer(resp) || isNegative(resp)
	case dns.RcodeNameError:
		return isNegative(resp)
	case dns.RcodeServerFailure:
		return true
	default:
		return false
	}
}

// hasIPAnswer returns true if the answer section of m contains an A or AAAA
// record.
func hasIPAnswer(m *dns.Msg) (ok bool) {
	return slices.ContainsFunc(m.Answer, func(rr dns.RR) (isIP bool) {
		t := rr.Header().Rrtype

		return t == dns.TypeA || t == dns.TypeAAAA
	})
}

// isNegative returns true if the authority section of m contains a SOA record
// and no NS records, so that it's a cacheable negative response.
func isNegative(m *dns.Msg) (ok bool) {
	for _, rr := range m.Ns {
		switch rr.Header().Rrtype {
		case dns.TypeSOA:
			ok = true
		case dns.TypeNS:
			return false
		default:
			// Go on.
		}
	}

	return ok
}

// minRRTTL returns the lowest TTL among the resource records of m except OPT.
// ok is false if there are no such records.
func minRRTTL(m *dns.Msg) (ttl uint32, ok bool) {
	ttl = math.MaxUint32
	for _, rrs := range [...][]dns.RR{m.Answer, m.Ns, m.Extra} {
		for _, rr := range rrs {
			h := rr.Header()
			if h.Rrtype != dns.TypeOPT {
				ttl, ok = min(ttl, h.Ttl), true
			}
		}
	}

	return ttl, ok
}

// Refresher refreshes single entries of the DNS cache.
type Refresher interface {
	// Refresh re-resolves k through the upstreams and stores the response in
	// the cache.  If the cached entry is still valid for at least minRemaining,
	// it doesn't resolve anything and returns false and the remaining TTL.
	// Otherwise, refreshed reports whether a response has been received, and
	// ttl is the duration for which it's cached, or zero if it isn't cached.
	// It returns [ErrInProgress] if k is being refreshed already.
	Refresh(
		ctx context.Context,
		k Key,
		minRemaining time.Duration,
	) (ttl time.Duration, refreshed bool, err error)
}

// ErrInProgress is returned by [Refresher.Refresh] when the key is already
// being refreshed elsewhere.
const ErrInProgress errors.Error = "refresh in progress"

// Counters are the statistics of a [Service].
type Counters struct {
	// Refreshed is the number of refreshes that received and cached a new
	// response.
	Refreshed uint64 `json:"refreshed"`

	// Failed is the number of refreshes that failed.
	Failed uint64 `json:"failed"`

	// Skipped is the number of refreshes that didn't fetch anything because
	// the entry was still fresh, was being refreshed elsewhere, or became
	// uncacheable.
	Skipped uint64 `json:"skipped"`

	// Tracked is the number of currently tracked cache keys.
	Tracked uint64 `json:"tracked"`
}

// Default values and constants of the refresh scheduling.
const (
	// DefaultTickInterval is the default interval between the checks for
	// entries that are due to be refreshed.
	DefaultTickInterval = 1 * time.Second

	// backoffBase is the delay before the first retry of a failed refresh.
	backoffBase = 10 * time.Second

	// backoffMax is the maximum delay between retries of a failed refresh.
	backoffMax = 10 * time.Minute

	// maxFailures is the number of consecutive failed refreshes after which a
	// key is no longer refreshed until the next client query for it.
	maxFailures = 5

	// inProgressRetry is the delay before the next attempt to refresh a key
	// that was being refreshed elsewhere.
	inProgressRetry = 5 * time.Second

	// hitBuckets is the number of hourly buckets in which the queries for a
	// key are counted.  It's one more than the number of hours in the window,
	// since the bucket of the current hour is only partially filled.
	hitBuckets = 24 + 1
)

// Limits of the configuration.
const (
	// MaxMaxConcurrent is the maximum allowed value of
	// [Config.MaxConcurrent].
	MaxMaxConcurrent = 1024

	// MaxMaxTracked is the maximum allowed value of [Config.MaxTracked].
	MaxMaxTracked = 100_000
)

// Config is the configuration of a [Service].
type Config struct {
	// Logger is used for logging the operation of the service.  It must not be
	// nil.
	Logger *slog.Logger

	// Clock is used to get the current time.  If nil, [timeutil.SystemClock] is
	// used.
	Clock timeutil.Clock

	// Refresher refreshes the cache entries.  It must not be nil.
	Refresher Refresher

	// Delay is how long after its expiry an ordinary entry is refreshed.  It
	// must not be negative.
	Delay time.Duration

	// PrefetchBefore is how long before its expiry an entry among the
	// PrefetchTop most queried ones is refreshed.  It must not be negative.
	PrefetchBefore time.Duration

	// OptimisticMaxAge is how long dnsproxy keeps expired entries.  Keys that
	// expired more than OptimisticMaxAge ago are no longer tracked.  It must be
	// positive.
	OptimisticMaxAge time.Duration

	// RefreshTimeout is the timeout of a single refresh.  It must be positive.
	RefreshTimeout time.Duration

	// TickInterval is the interval between the checks for due entries.  If
	// zero, [DefaultTickInterval] is used.
	TickInterval time.Duration

	// MaxConcurrent is the maximum number of simultaneous refreshes.  It must
	// be positive.
	MaxConcurrent uint32

	// MinHits is the number of queries in the last 24 hours that a key must
	// exceed to be refreshed.
	MinHits uint32

	// PrefetchTop is the number of the most queried keys that are refreshed
	// before their expiry instead of after it.
	PrefetchTop uint32

	// MaxTracked is the maximum number of tracked keys.  The least recently
	// queried keys are evicted beyond it.  It must be positive.
	MaxTracked uint32
}

// Service refreshes popular entries of the DNS cache in the background.
type Service struct {
	logger    *slog.Logger
	clock     timeutil.Clock
	refresher Refresher
	tracker   *tracker

	// sema limits the number of simultaneous refreshes.
	sema chan struct{}

	// done is closed on shutdown.
	done chan struct{}

	// loopDone is closed when the scheduling loop exits.
	loopDone chan struct{}

	// cancel cancels the context of running refreshes.
	cancel context.CancelFunc

	// wg tracks the running refreshes.
	wg sync.WaitGroup

	// stopOnce makes sure that shutdown happens once.
	stopOnce sync.Once

	// started is true after [Service.Start].
	started atomic.Bool

	refreshed atomic.Uint64
	failed    atomic.Uint64
	skipped   atomic.Uint64

	delay            time.Duration
	prefetchBefore   time.Duration
	optimisticMaxAge time.Duration
	refreshTimeout   time.Duration
	tickInterval     time.Duration
	minHits          uint32
	prefetchTop      uint32
}

// New returns a new properly initialized *Service.  c must not be nil and must
// be valid.
func New(c *Config) (s *Service) {
	clock := c.Clock
	if clock == nil {
		clock = timeutil.SystemClock{}
	}

	tickInterval := c.TickInterval
	if tickInterval <= 0 {
		tickInterval = DefaultTickInterval
	}

	return &Service{
		logger:           c.Logger,
		clock:            clock,
		refresher:        c.Refresher,
		tracker:          newTracker(c.MaxTracked),
		sema:             make(chan struct{}, c.MaxConcurrent),
		done:             make(chan struct{}),
		loopDone:         make(chan struct{}),
		cancel:           func() {},
		delay:            c.Delay,
		prefetchBefore:   c.PrefetchBefore,
		optimisticMaxAge: c.OptimisticMaxAge,
		refreshTimeout:   c.RefreshTimeout,
		tickInterval:     tickInterval,
		minHits:          c.MinHits,
		prefetchTop:      c.PrefetchTop,
	}
}

// Start starts the background refreshing.  It must be called only once.
func (s *Service) Start(_ context.Context) (err error) {
	// Don't inherit the context of the caller, since it's usually the one of
	// the start-up procedure.
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.started.Store(true)

	go s.run(ctx)

	return nil
}

// Shutdown stops the background refreshing.  It doesn't wait for the running
// refreshes, since upstream exchanges can't be cancelled and a refresh may take
// several upstream timeouts.  Their results are discarded.
func (s *Service) Shutdown(ctx context.Context) (err error) {
	if !s.started.Load() {
		return nil
	}

	s.stopOnce.Do(func() {
		close(s.done)
		s.cancel()
	})

	select {
	case <-s.loopDone:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("waiting for scheduling loop: %w", context.Cause(ctx))
	}
}

// Hit records a resolved client query for k.  ttl is the validity of the
// response, see [ResponseTTL], and cached is true if the response was served
// from the cache.  A cached response may be an expired one served
// optimistically, so its TTL doesn't tell when the cached entry expires.  It's
// safe for concurrent use.
func (s *Service) Hit(k Key, ttl time.Duration, cached bool) {
	s.tracker.hit(k, ttl, cached, s.clock.Now())
}

// Clear stops tracking all keys.  It should be called when the cache is
// cleared.
func (s *Service) Clear() {
	s.tracker.clear()
}

// Counters returns the current statistics of the service.
func (s *Service) Counters() (c Counters) {
	return Counters{
		Refreshed: s.refreshed.Load(),
		Failed:    s.failed.Load(),
		Skipped:   s.skipped.Load(),
		Tracked:   uint64(s.tracker.len()),
	}
}

// run checks for due entries every tick interval until the service is shut
// down.  It's intended to be used as a goroutine.
func (s *Service) run(ctx context.Context) {
	defer close(s.loopDone)
	defer slogutil.RecoverAndLog(ctx, s.logger)

	t := time.NewTicker(s.tickInterval)
	defer t.Stop()

	for {
		select {
		case <-t.C:
			s.tick(ctx, s.clock.Now())
		case <-s.done:
			return
		}
	}
}
