package anytlsupstream

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"math/big"
	"net"
	"net/netip"
	"net/url"
	"testing"
	"time"

	"github.com/AdguardTeam/dnsproxy/upstream"
	anytls "github.com/anytls/sing-anytls"
	"github.com/anytls/sing-anytls/padding"
	"github.com/miekg/dns"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testPassword = "secret"

var testAnswerIP = netip.MustParseAddr("192.0.2.1")

type discardHandler struct{}

func (discardHandler) NewConnectionEx(
	_ context.Context,
	conn net.Conn,
	_ M.Socksaddr,
	_ M.Socksaddr,
	_ N.CloseHandlerFunc,
) {
	_ = conn.Close()
}

// answerA answers every query with testAnswerIP, as an AnyTLS inbound with
// dns enabled answers with its own DNS.
func answerA(_ context.Context, request []byte) (response []byte, err error) {
	req := &dns.Msg{}
	err = req.Unpack(request)
	if err != nil {
		return nil, err
	}

	resp := (&dns.Msg{}).SetReply(req)
	resp.Answer = []dns.RR{&dns.A{
		Hdr: dns.RR_Header{
			Name:   req.Question[0].Name,
			Rrtype: dns.TypeA,
			Class:  dns.ClassINET,
			Ttl:    60,
		},
		A: testAnswerIP.AsSlice(),
	}}

	return resp.Pack()
}

// startServer starts an AnyTLS server on a loopback TLS listener and returns
// its address and the pin of its public key.  dnsHandler may be nil, then the
// server does not offer DNS.
func startServer(t *testing.T, dnsHandler func(context.Context, []byte) ([]byte, error)) (addr, pin string) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "anytls.test"},
		DNSNames:     []string{"anytls.test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)

	pubKey, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	require.NoError(t, err)

	sum := sha256.Sum256(pubKey)
	pin = base64.StdEncoding.EncodeToString(sum[:])

	service, err := anytls.NewService(anytls.ServiceConfig{
		Users:         []anytls.User{{Name: "user", Password: testPassword}},
		PaddingScheme: padding.DefaultPaddingScheme,
		Handler:       discardHandler{},
		Logger:        logger.NOP(),
		DNSHandler:    dnsHandler,
	})
	require.NoError(t, err)

	l, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })

	go func() {
		for {
			conn, aerr := l.Accept()
			if aerr != nil {
				return
			}

			go func() { _ = service.NewConnection(context.Background(), conn, M.Socksaddr{}, nil) }()
		}
	}()

	return l.Addr().String(), pin
}

func newTestUpstream(t *testing.T, addr string) (u upstream.Upstream) {
	t.Helper()

	u, err := upstream.AddressToUpstream(addr, &upstream.Options{Timeout: 5 * time.Second})
	require.NoError(t, err)
	t.Cleanup(func() { _ = u.Close() })

	return u
}

func exchangeA(t *testing.T, u upstream.Upstream) (resp *dns.Msg, err error) {
	t.Helper()

	req := (&dns.Msg{}).SetQuestion("example.org.", dns.TypeA)

	resp, err = u.Exchange(req)
	if err == nil {
		assert.Equal(t, req.Id, resp.Id)
	}

	return resp, err
}

func TestUpstream_Exchange(t *testing.T) {
	t.Parallel()

	addr, pin := startServer(t, answerA)

	t.Run("insecure", func(t *testing.T) {
		u := newTestUpstream(t, "anytls://"+testPassword+"@"+addr+"?insecure=1")

		resp, err := exchangeA(t, u)
		require.NoError(t, err)
		require.Len(t, resp.Answer, 1)

		a, ok := resp.Answer[0].(*dns.A)
		require.True(t, ok)
		assert.Equal(t, testAnswerIP.AsSlice(), []byte(a.A.To4()))
	})

	t.Run("pinned", func(t *testing.T) {
		u := newTestUpstream(
			t,
			"anytls://"+testPassword+"@"+addr+"?sni=anytls.test&certificate_public_key_sha256="+
				url.QueryEscape(pin),
		)

		_, err := exchangeA(t, u)
		require.NoError(t, err)
	})

	t.Run("wrong_pin", func(t *testing.T) {
		wrong := base64.StdEncoding.EncodeToString(make([]byte, sha256.Size))
		u := newTestUpstream(
			t,
			"anytls://"+testPassword+"@"+addr+"?certificate_public_key_sha256="+url.QueryEscape(wrong),
		)

		_, err := exchangeA(t, u)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unrecognized remote public key")
	})

	t.Run("untrusted_certificate", func(t *testing.T) {
		u := newTestUpstream(t, "anytls://"+testPassword+"@"+addr+"?sni=anytls.test")

		_, err := exchangeA(t, u)
		require.Error(t, err)
	})
}

func TestUpstream_Exchange_unsupported(t *testing.T) {
	t.Parallel()

	addr, _ := startServer(t, nil)
	u := newTestUpstream(t, "anytls://"+testPassword+"@"+addr+"?insecure=1")

	_, err := exchangeA(t, u)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not answer DNS")
}

func TestUpstream_Address(t *testing.T) {
	t.Parallel()

	u := newTestUpstream(t, "anytls://hidden@[2001:db8::1]:8443/?sni=x&insecure=1#node")
	assert.Equal(t, "anytls://[2001:db8::1]:8443", u.Address())
	assert.NotContains(t, u.Address(), "hidden")
}

func TestPoolSharing(t *testing.T) {
	t.Parallel()

	const addr = "anytls://shared@pool.test:8443?heartbeat=10s"

	a := newTestUpstream(t, addr)
	b := newTestUpstream(t, addr)
	c := newTestUpstream(t, "anytls://shared@pool.test:8443?heartbeat=20s")

	pa, pb, pc := a.(*Upstream).pool, b.(*Upstream).pool, c.(*Upstream).pool
	assert.Same(t, pa, pb)
	assert.NotSame(t, pa, pc)

	require.NoError(t, a.Close())
	require.NoError(t, a.Close())

	poolsMu.Lock()
	assert.Equal(t, 1, pa.refs)
	poolsMu.Unlock()

	require.NoError(t, b.Close())

	poolsMu.Lock()
	assert.Equal(t, 0, pa.refs)
	assert.NotNil(t, pa.closeTimer)
	poolsMu.Unlock()

	// A new upstream within the release delay takes the pool back.
	d := newTestUpstream(t, addr)
	assert.Same(t, pa, d.(*Upstream).pool)

	poolsMu.Lock()
	assert.Nil(t, pa.closeTimer)
	poolsMu.Unlock()
}
