// Package anytlsupstream implements DNS upstreams that send queries inside the
// sessions of an AnyTLS client, where the server answers them with its own
// DNS.  It matches the anytls DNS server of the KexiChanProjectProxy sing-box
// fork, and the server must enable dns on its AnyTLS inbound.
//
// Importing the package registers the anytls URL scheme with
// [upstream.AddressToUpstream].
package anytlsupstream

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/AdguardTeam/dnsproxy/upstream"
	anytls "github.com/anytls/sing-anytls"
	"github.com/miekg/dns"
)

func init() {
	upstream.RegisterScheme(Scheme, New)
}

// Upstream is an AnyTLS DNS upstream.
type Upstream struct {
	pool      *pool
	addr      string
	timeout   time.Duration
	closeOnce sync.Once
}

var _ upstream.Upstream = (*Upstream)(nil)

// New returns an upstream for the AnyTLS address uu, see [config].  Upstreams
// with the same address share one AnyTLS client and its sessions.
func New(uu *url.URL, opts *upstream.Options) (u upstream.Upstream, err error) {
	conf, err := parseConfig(uu)
	if err != nil {
		return nil, err
	}

	p, err := acquirePool(conf, opts)
	if err != nil {
		return nil, err
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}

	return &Upstream{
		pool: p,
		// The address shows in the logs, the query log and the statistics, so
		// it leaves out the password and the parameters.
		addr:    (&url.URL{Scheme: Scheme, Host: conf.hostPort()}).String(),
		timeout: timeout,
	}, nil
}

// Address implements the [upstream.Upstream] interface for *Upstream.
func (u *Upstream) Address() (addr string) {
	return u.addr
}

// Exchange implements the [upstream.Upstream] interface for *Upstream.
func (u *Upstream) Exchange(req *dns.Msg) (resp *dns.Msg, err error) {
	packed, err := req.Pack()
	if err != nil {
		return nil, fmt.Errorf("packing request: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), u.timeout)
	defer cancel()

	raw, err := u.pool.client.ExchangeDNS(ctx, packed)
	if err != nil {
		if errors.Is(err, anytls.ErrDNSUnsupported) {
			return nil, fmt.Errorf(
				"anytls server %s does not answer DNS: enable dns on its anytls inbound",
				u.pool.conf.hostPort(),
			)
		}

		return nil, fmt.Errorf("exchanging with %s: %w", u.addr, err)
	}

	resp = &dns.Msg{}
	err = resp.Unpack(raw)
	if err != nil {
		return nil, fmt.Errorf("unpacking response from %s: %w", u.addr, err)
	}

	if resp.Id != req.Id {
		return nil, fmt.Errorf("response from %s: %w", u.addr, dns.ErrId)
	}

	return resp, nil
}

// Close implements the [upstream.Upstream] interface for *Upstream.  The
// shared AnyTLS client is closed shortly after its last upstream.
func (u *Upstream) Close() (err error) {
	u.closeOnce.Do(func() {
		releasePool(u.pool)
	})

	return nil
}
