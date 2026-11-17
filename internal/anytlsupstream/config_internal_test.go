package anytlsupstream

import (
	"crypto/tls"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseConfig(t *testing.T) {
	t.Parallel()

	const pin = "47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU="

	testCases := []struct {
		check   func(t *testing.T, c *config)
		name    string
		addr    string
		wantErr string
	}{{
		name: "minimal",
		addr: "anytls://secret@example.org",
		check: func(t *testing.T, c *config) {
			assert.Equal(t, "example.org", c.host)
			assert.Equal(t, uint16(443), c.port)
			assert.Equal(t, "secret", c.password)
			assert.Empty(t, c.serverName)
			assert.False(t, c.insecure)
		},
	}, {
		name: "share_link",
		addr: "anytls://p%40ss%3Aword@[2001:db8::1]:8443/?sni=front.example&insecure=1#node",
		check: func(t *testing.T, c *config) {
			assert.Equal(t, "2001:db8::1", c.host)
			assert.Equal(t, uint16(8443), c.port)
			assert.Equal(t, "p@ss:word", c.password)
			assert.Equal(t, "front.example", c.serverName)
			assert.True(t, c.insecure)
			assert.Equal(t, "[2001:db8::1]:8443", c.hostPort())
		},
	}, {
		name: "unescaped_colon",
		addr: "anytls://a:b@example.org",
		check: func(t *testing.T, c *config) {
			assert.Equal(t, "a:b", c.password)
		},
	}, {
		name: "sing_box_fields",
		addr: "anytls://secret@example.org:443?server_name=sni.example&alpn=h2,http/1.1" +
			"&min_version=1.2&max_version=1.3&certificate_public_key_sha256=" + url.QueryEscape(pin) +
			"&idle_session_check_interval=20s&idle_session_timeout=40s&min_idle_session=1" +
			"&ensure_idle_session=2&min_idle_session_for_age=3&ensure_idle_session_create_rate=4" +
			"&heartbeat=15s&max_connection_lifetime=5m&connection_lifetime_jitter=10s" +
			"&close_after_idle=1m&close_after_lifetime=1h&client_metadata=agh",
		check: func(t *testing.T, c *config) {
			assert.Equal(t, "sni.example", c.serverName)
			assert.Equal(t, []string{"h2", "http/1.1"}, c.alpn)
			assert.Equal(t, uint16(tls.VersionTLS12), c.minVersion)
			assert.Equal(t, uint16(tls.VersionTLS13), c.maxVersion)
			require.Len(t, c.pins, 1)

			cc := c.client
			assert.Equal(t, 20*time.Second, cc.IdleSessionCheckInterval)
			assert.Equal(t, 40*time.Second, cc.IdleSessionTimeout)
			assert.Equal(t, 1, cc.MinIdleSession)
			assert.Equal(t, 2, cc.EnsureIdleSession)
			assert.Equal(t, 3, cc.MinIdleSessionForAge)
			assert.Equal(t, 4, cc.EnsureIdleSessionCreateRate)
			assert.Equal(t, 15*time.Second, cc.Heartbeat)
			assert.Equal(t, 5*time.Minute, cc.MaxConnectionLifetime)
			assert.Equal(t, 10*time.Second, cc.ConnectionLifetimeJitter)
			assert.Equal(t, time.Minute, cc.CloseAfterIdle)
			assert.Equal(t, time.Hour, cc.CloseAfterLifetime)
			assert.Equal(t, "agh", cc.ClientMetadata)
		},
	}, {
		name:    "no_password",
		addr:    "anytls://example.org",
		wantErr: "missing password",
	}, {
		name:    "unknown_param",
		addr:    "anytls://secret@example.org?idle_timeout=30s",
		wantErr: `parameter "idle_timeout": unknown parameter`,
	}, {
		name:    "repeated_param",
		addr:    "anytls://secret@example.org?sni=a&sni=b",
		wantErr: "want one value",
	}, {
		name:    "bad_duration",
		addr:    "anytls://secret@example.org?heartbeat=30",
		wantErr: `parameter "heartbeat"`,
	}, {
		name:    "bad_bool",
		addr:    "anytls://secret@example.org?insecure=yes",
		wantErr: "invalid boolean",
	}, {
		name:    "short_pin",
		addr:    "anytls://secret@example.org?certificate_public_key_sha256=AAAA",
		wantErr: "want 32",
	}, {
		name:    "versions",
		addr:    "anytls://secret@example.org?min_version=1.3&max_version=1.2",
		wantErr: "min_version is above max_version",
	}, {
		name:    "path",
		addr:    "anytls://secret@example.org/dns-query",
		wantErr: "unexpected path",
	}}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			uu, err := url.Parse(tc.addr)
			require.NoError(t, err)

			c, err := parseConfig(uu)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)

				return
			}

			require.NoError(t, err)
			tc.check(t, c)
		})
	}
}

func TestParseConfig_key(t *testing.T) {
	t.Parallel()

	parse := func(addr string) (c *config) {
		uu, err := url.Parse(addr)
		require.NoError(t, err)

		c, err = parseConfig(uu)
		require.NoError(t, err)

		return c
	}

	a := parse("anytls://secret@example.org?sni=x&heartbeat=10s#a")
	b := parse("anytls://secret@example.org:443/?heartbeat=10s&sni=x#b")
	assert.Equal(t, a.key, b.key)

	c := parse("anytls://other@example.org?sni=x&heartbeat=10s")
	assert.NotEqual(t, a.key, c.key)
	assert.True(t, strings.Contains(a.key, "secret"))
}
