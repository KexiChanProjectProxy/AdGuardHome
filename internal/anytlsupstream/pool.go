package anytlsupstream

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/AdguardTeam/dnsproxy/upstream"
	anytls "github.com/anytls/sing-anytls"
)

// releaseDelay is how long a pool no upstream uses stays open, so that the
// upstreams created when the configuration is reloaded reuse its sessions.
const releaseDelay = 10 * time.Second

// defaultTimeout is the timeout of queries and dials when the options set
// none, as the sing-box DNS timeout.
const defaultTimeout = 10 * time.Second

// dialOptions are the parts of the upstream options that a pool uses when it
// dials.  The most recently created upstream sets them, so that a pool shared
// across reloads follows the current settings.
type dialOptions struct {
	bootstrap        upstream.Resolver
	rootCAs          *x509.CertPool
	verifyServerCert func(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error
	verifyConnection func(state tls.ConnectionState) error
	cipherSuites     []uint16
	timeout          time.Duration
	insecure         bool
	preferIPv6       bool
}

// newDialOptions returns the dial options from opts.
func newDialOptions(opts *upstream.Options) (do *dialOptions) {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}

	return &dialOptions{
		bootstrap:        opts.Bootstrap,
		rootCAs:          opts.RootCAs,
		verifyServerCert: opts.VerifyServerCertificate,
		verifyConnection: opts.VerifyConnection,
		cipherSuites:     opts.CipherSuites,
		timeout:          timeout,
		insecure:         opts.InsecureSkipVerify,
		preferIPv6:       opts.PreferIPv6,
	}
}

// pool is an AnyTLS client with its session pool, shared by every upstream
// with the same address.
type pool struct {
	conf   *config
	client *anytls.Client
	opts   atomic.Pointer[dialOptions]
	logger *slog.Logger

	// refs and closeTimer are protected by poolsMu.
	refs       int
	closeTimer *time.Timer
}

var (
	poolsMu sync.Mutex
	pools   = map[string]*pool{}
)

// acquirePool returns the pool for conf, creating it when there is none, and
// makes it dial with opts from now on.  Each call must be paired with a call
// to [releasePool].
func acquirePool(conf *config, opts *upstream.Options) (p *pool, err error) {
	poolsMu.Lock()
	defer poolsMu.Unlock()

	p = pools[conf.key]
	if p == nil {
		p = &pool{
			conf:   conf,
			logger: opts.Logger.With("anytls", conf.hostPort()),
		}

		clientConf := conf.client
		clientConf.Password = conf.password
		clientConf.DialOut = p.dialOut
		clientConf.Logger = &singLogger{logger: p.logger}

		p.client, err = anytls.NewClient(context.Background(), clientConf)
		if err != nil {
			return nil, fmt.Errorf("anytls: creating client: %w", err)
		}

		pools[conf.key] = p
	}

	p.opts.Store(newDialOptions(opts))
	p.refs++
	if p.closeTimer != nil {
		p.closeTimer.Stop()
		p.closeTimer = nil
	}

	return p, nil
}

// releasePool gives up a reference to p taken by [acquirePool].  The last
// release closes p after [releaseDelay] unless it is acquired again.
func releasePool(p *pool) {
	poolsMu.Lock()
	defer poolsMu.Unlock()

	p.refs--
	if p.refs > 0 {
		return
	}

	p.closeTimer = time.AfterFunc(releaseDelay, func() {
		poolsMu.Lock()
		if p.refs > 0 || pools[p.conf.key] != p {
			poolsMu.Unlock()

			return
		}

		delete(pools, p.conf.key)
		poolsMu.Unlock()

		err := p.client.Close()
		if err != nil {
			p.logger.Debug("closing anytls client", "err", err)
		}
	})
}

// dialOut connects to the server and completes the TLS handshake.  It is the
// DialOut function of the AnyTLS client.
func (p *pool) dialOut(ctx context.Context) (conn net.Conn, err error) {
	opts := p.opts.Load()

	ctx, cancel := context.WithTimeout(ctx, opts.timeout)
	defer cancel()

	addrs, err := p.resolve(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("resolving %s: %w", p.conf.host, err)
	}

	var errs []error
	dialer := &net.Dialer{}
	port := p.conf.port
	for _, addr := range addrs {
		var raw net.Conn
		raw, err = dialer.DialContext(ctx, "tcp", netip.AddrPortFrom(addr, port).String())
		if err != nil {
			errs = append(errs, err)
			if ctx.Err() != nil {
				break
			}

			continue
		}

		tlsConn := tls.Client(raw, p.tlsConfig(opts))
		err = tlsConn.HandshakeContext(ctx)
		if err != nil {
			_ = raw.Close()

			return nil, fmt.Errorf("tls handshake with %s: %w", p.conf.hostPort(), err)
		}

		return tlsConn, nil
	}

	return nil, fmt.Errorf("connecting to %s: %w", p.conf.hostPort(), joinErrors(errs))
}

// resolve returns the addresses of the server to try in order.
func (p *pool) resolve(ctx context.Context, opts *dialOptions) (addrs []netip.Addr, err error) {
	host := p.conf.host
	if addr, perr := netip.ParseAddr(host); perr == nil {
		return []netip.Addr{addr.Unmap()}, nil
	}

	if opts.bootstrap != nil {
		addrs, err = opts.bootstrap.LookupNetIP(ctx, "ip", host)
	} else {
		addrs, err = net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	}
	if err != nil {
		return nil, err
	}

	if len(addrs) == 0 {
		return nil, fmt.Errorf("no addresses")
	}

	addrs = slices.Clone(addrs)
	for i, a := range addrs {
		addrs[i] = a.Unmap()
	}

	slices.SortStableFunc(addrs, func(a, b netip.Addr) int {
		if a.Is6() == b.Is6() {
			return 0
		}

		if a.Is6() == opts.preferIPv6 {
			return -1
		}

		return 1
	})

	return addrs, nil
}

// tlsConfig returns the TLS configuration of a new connection, built as the
// sing-box standard TLS client builds it.
func (p *pool) tlsConfig(opts *dialOptions) (tc *tls.Config) {
	conf := p.conf
	tc = &tls.Config{
		ServerName:         conf.serverName,
		RootCAs:            opts.rootCAs,
		CipherSuites:       opts.cipherSuites,
		NextProtos:         conf.alpn,
		MinVersion:         conf.minVersion,
		MaxVersion:         conf.maxVersion,
		InsecureSkipVerify: conf.insecure || opts.insecure,
		VerifyConnection:   opts.verifyConnection,
	}
	if tc.ServerName == "" {
		tc.ServerName = conf.host
	}

	if len(conf.pins) > 0 {
		// The pins replace the verification of the certificate chain.
		pins := conf.pins
		tc.InsecureSkipVerify = true
		tc.VerifyPeerCertificate = func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			return verifyPins(pins, rawCerts)
		}
	} else if !tc.InsecureSkipVerify {
		tc.VerifyPeerCertificate = opts.verifyServerCert
	}

	return tc
}

// joinErrors returns the errors joined, or a generic error when there are
// none.
func joinErrors(errs []error) (err error) {
	if len(errs) == 0 {
		return fmt.Errorf("no addresses to try")
	}

	return errors.Join(errs...)
}
