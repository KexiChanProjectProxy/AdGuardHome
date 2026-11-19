package dnsforward

import (
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AdguardTeam/AdGuardHome/internal/aghtest"
	"github.com/AdguardTeam/AdGuardHome/internal/filtering"
	"github.com/AdguardTeam/dnsproxy/proxy"
	"github.com/AdguardTeam/dnsproxy/upstream"
	"github.com/AdguardTeam/golibs/testutil"
	"github.com/AdguardTeam/golibs/timeutil"
	"github.com/miekg/dns"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newCacheRefreshTestConfig returns a server configuration with the cache, the
// optimistic cache, and the proactive cache refresh enabled.
func newCacheRefreshTestConfig() (conf ServerConfig) {
	return ServerConfig{
		UDPListenAddrs: []*net.UDPAddr{{}},
		TCPListenAddrs: []*net.TCPAddr{{}},
		TLSConf:        &TLSConfig{},
		Config: Config{
			UpstreamMode:     UpstreamModeLoadBalance,
			EDNSClientSubnet: &EDNSClientSubnet{Enabled: false},
			ClientsContainer: EmptyClientsContainer{},

			CacheEnabled:          true,
			CacheSize:             1 << 20,
			CacheOptimistic:       true,
			CacheOptimisticMaxAge: timeutil.Duration(time.Hour),

			CacheRefreshEnabled:       true,
			CacheRefreshMaxConcurrent: 1,
			CacheRefreshDelay:         0,
			CacheRefreshMinHits:       0,
			CacheRefreshMaxTracked:    10,
		},
		ServePlainDNS: true,
	}
}

func TestServer_cacheRefresh(t *testing.T) {
	const host = "refresh.example"

	var exchanges atomic.Int32
	ups := aghtest.NewUpstreamMock(func(req *dns.Msg) (resp *dns.Msg, err error) {
		exchanges.Add(1)

		resp = aghtest.MatchedResponse(req, dns.TypeA, host, "1.2.3.4")
		resp.Answer[0].Header().Ttl = 1

		return resp, nil
	})

	s := createTestServer(t, &filtering.Config{
		BlockingMode: filtering.BlockingModeDefault,
	}, newCacheRefreshTestConfig(), testTLSConfigProvider)
	s.conf.UpstreamConfig.Upstreams = []upstream.Upstream{ups}

	startDeferStop(t, s)
	addr := s.dnsProxy.Addr(proxy.ProtoUDP).String()

	reply, err := dns.Exchange(createTestMessage(host+"."), addr)
	require.NoError(t, err)
	require.NotEmpty(t, reply.Answer)
	require.Equal(t, int32(1), exchanges.Load())

	// The entry is refreshed without any further client queries.
	require.Eventually(t, func() (ok bool) {
		return exchanges.Load() >= 2
	}, 5*time.Second, 50*time.Millisecond)

	c, ok := s.CacheRefreshCounters()
	require.True(t, ok)

	assert.Positive(t, c.Refreshed)
	assert.Equal(t, uint64(1), c.Tracked)

	w := httptest.NewRecorder()
	s.handleCacheClear(w, httptest.NewRequest(http.MethodPost, "/control/cache_clear", nil))
	require.Equal(t, http.StatusOK, w.Code)

	c, _ = s.CacheRefreshCounters()
	assert.Zero(t, c.Tracked)
}

func TestServer_newCacheRefresh(t *testing.T) {
	testCases := []struct {
		modify  func(c *ServerConfig)
		name    string
		wantErr string
		wantOn  bool
	}{{
		modify:  func(_ *ServerConfig) {},
		name:    "enabled",
		wantErr: "",
		wantOn:  true,
	}, {
		modify:  func(c *ServerConfig) { c.CacheRefreshEnabled = false },
		name:    "disabled",
		wantErr: "",
		wantOn:  false,
	}, {
		modify:  func(c *ServerConfig) { c.CacheOptimistic = false },
		name:    "not_optimistic",
		wantErr: "",
		wantOn:  false,
	}, {
		modify: func(c *ServerConfig) { c.CacheRefreshMaxConcurrent = 0 },
		name:   "bad_max_concurrent",
		wantErr: "preparing proxy: invalid cache refresh settings: " +
			"cache_refresh_max_concurrent: out of range: must be no less than 1, got 0",
		wantOn: false,
	}}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			conf := newCacheRefreshTestConfig()
			tc.modify(&conf)

			s, err := NewServer(DNSCreateParams{
				Logger:            testLogger,
				TLSConfigProvider: testTLSConfigProvider,
			})
			require.NoError(t, err)

			err = s.Prepare(testutil.ContextWithTimeout(t, testTimeout), &conf)
			testutil.AssertErrorMsg(t, tc.wantErr, err)
			if err != nil {
				return
			}

			assert.Equal(t, tc.wantOn, s.cacheRefresh.Load() != nil)
		})
	}
}
