package dnsforward

import (
	"context"
	"net/netip"
	"time"

	"github.com/AdguardTeam/AdGuardHome/internal/dnsforward/cacherefresh"
	"github.com/AdguardTeam/dnsproxy/proxy"
	"github.com/AdguardTeam/golibs/errors"
	"github.com/AdguardTeam/golibs/logutil/slogutil"
)

// proxyRefresher is a [cacherefresh.Refresher] that refreshes the cache of a
// dnsproxy instance.  It holds the proxy directly instead of using
// [Server.proxy], since the refreshes are waited for while s.serverLock is
// locked.
type proxyRefresher struct {
	prx *proxy.Proxy
}

// type check
var _ cacherefresh.Refresher = (*proxyRefresher)(nil)

// Refresh implements the [cacherefresh.Refresher] interface for
// *proxyRefresher.
func (r *proxyRefresher) Refresh(
	ctx context.Context,
	k cacherefresh.Key,
	minRemaining time.Duration,
) (ttl time.Duration, refreshed bool, err error) {
	dctx := &proxy.DNSContext{
		Proto: proxy.ProtoUDP,
		Req:   k.NewRequest(),
	}

	ttl, refreshed, err = r.prx.RefreshCache(ctx, dctx, minRemaining)
	if errors.Is(err, proxy.ErrCacheRefreshInProgress) {
		return 0, false, cacherefresh.ErrInProgress
	}

	return ttl, refreshed, err
}

// newCacheRefresh returns a new proactive cache refresh service for prx, or nil
// if the proactive refresh is disabled.  s.serverLock is expected to be locked.
func (s *Server) newCacheRefresh(ctx context.Context, prx *proxy.Proxy) (cr *cacherefresh.Service) {
	c := &s.conf
	if !c.CacheRefreshEnabled {
		return nil
	}

	if !c.CacheEnabled || !c.CacheOptimistic {
		s.logger.WarnContext(
			ctx,
			"cache_refresh_enabled is true, but cache_enabled or cache_optimistic is false; "+
				"proactive cache refresh is disabled",
		)

		return nil
	}

	timeout := c.UpstreamTimeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	maxAge := time.Duration(c.CacheOptimisticMaxAge)
	if maxAge <= 0 {
		maxAge = proxy.DefaultOptimisticMaxAge
	}

	return cacherefresh.New(&cacherefresh.Config{
		Logger:           s.baseLogger.With(slogutil.KeyPrefix, "cacherefresh"),
		Refresher:        &proxyRefresher{prx: prx},
		Delay:            time.Duration(c.CacheRefreshDelay),
		PrefetchBefore:   time.Duration(c.CacheRefreshPrefetchBefore),
		OptimisticMaxAge: maxAge,
		RefreshTimeout:   timeout,
		MaxConcurrent:    c.CacheRefreshMaxConcurrent,
		MinHits:          c.CacheRefreshMinHits,
		PrefetchTop:      c.CacheRefreshPrefetchTop,
		MaxTracked:       c.CacheRefreshMaxTracked,
	})
}

// shutdownCacheRefresh stops the proactive cache refresh service, if any.
func (s *Server) shutdownCacheRefresh(ctx context.Context) {
	cr := s.cacheRefresh.Swap(nil)
	if cr == nil {
		return
	}

	err := cr.Shutdown(ctx)
	if err != nil {
		s.logger.ErrorContext(ctx, "shutting down cache refresh", slogutil.KeyError, err)
	}
}

// observeForCacheRefresh records the client query resolved by dnsproxy for the
// proactive cache refresh.  pctx must have been passed to [proxy.Proxy.Resolve]
// and must not be modified by the response filtering yet.
func (s *Server) observeForCacheRefresh(pctx *proxy.DNSContext) {
	cr := s.cacheRefresh.Load()
	if cr == nil ||
		pctx.Res == nil ||
		pctx.ReqECS != nil ||
		pctx.CustomUpstreamConfig != nil ||
		pctx.RequestedPrivateRDNS != (netip.Prefix{}) ||
		pctx.Req.CheckingDisabled {
		// Only the general cache of the proxy is refreshed.
		return
	}

	k, ok := cacherefresh.KeyFromMsg(pctx.Req)
	if !ok {
		return
	}

	cr.Hit(k, cacherefresh.ResponseTTL(pctx.Res), isCachedResponse(pctx))
}

// isCachedResponse returns true if the response in pctx was served from the
// cache.
func isCachedResponse(pctx *proxy.DNSContext) (ok bool) {
	qs := pctx.QueryStatistics()
	if qs == nil {
		return false
	}

	ms := qs.Main()

	return len(ms) == 1 && ms[0].IsCached
}

// clearCacheRefresh forgets the keys tracked for the proactive cache refresh.
func (s *Server) clearCacheRefresh() {
	if cr := s.cacheRefresh.Load(); cr != nil {
		cr.Clear()
	}
}

// CacheRefreshCounters returns the statistics of the proactive cache refresh.
// ok is false if it is disabled.
func (s *Server) CacheRefreshCounters() (c cacherefresh.Counters, ok bool) {
	cr := s.cacheRefresh.Load()
	if cr == nil {
		return cacherefresh.Counters{}, false
	}

	return cr.Counters(), true
}
