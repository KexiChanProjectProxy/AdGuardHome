package anytlsupstream

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	anytls "github.com/anytls/sing-anytls"
)

// Scheme is the URL scheme of AnyTLS upstreams.
const Scheme = "anytls"

// defaultPort is the server port used when the address has none.
const defaultPort = 443

// config is an AnyTLS upstream address parsed from a URL of the form
//
//	anytls://password@host[:port][/][?param=value&...][#name]
//
// The parameters are named as the fields of the sing-box AnyTLS outbound and
// its TLS options, plus the sni and insecure parameters of the AnyTLS URI
// scheme.
type config struct {
	// host is the server host name or IP address, without brackets.
	host string

	// password is the AnyTLS password.
	password string

	// key identifies the session pool shared by upstreams with the same
	// address.  It contains the password.
	key string

	// serverName is the TLS server name, host when not set.
	serverName string

	// alpn is the TLS application protocol list.
	alpn []string

	// pins are the SHA-256 hashes of the accepted server public keys.
	pins [][]byte

	// client are the session pool settings.
	client anytls.ClientConfig

	// port is the server port.
	port uint16

	minVersion uint16
	maxVersion uint16

	// insecure disables the verification of the server certificate.
	insecure bool
}

// hostPort returns the server address.
func (c *config) hostPort() (hp string) {
	return net.JoinHostPort(c.host, strconv.Itoa(int(c.port)))
}

// parseConfig parses an AnyTLS upstream URL.  uu must have the AnyTLS scheme
// and a valid host, see [upstream.AddressToUpstream].
func parseConfig(uu *url.URL) (c *config, err error) {
	c = &config{}

	if uu.Opaque != "" {
		return nil, fmt.Errorf("anytls: invalid address: want %s://password@host:port", Scheme)
	}

	if uu.User != nil {
		// The AnyTLS URI scheme puts the password in the user name, so a colon
		// in an unescaped password splits it.
		c.password = uu.User.Username()
		if p, ok := uu.User.Password(); ok {
			c.password += ":" + p
		}
	}
	if c.password == "" {
		return nil, fmt.Errorf("anytls: missing password: want %s://password@host:port", Scheme)
	}

	if uu.Path != "" && uu.Path != "/" {
		return nil, fmt.Errorf("anytls: unexpected path %q", uu.Path)
	}

	c.host = uu.Hostname()
	if c.host == "" {
		return nil, fmt.Errorf("anytls: missing server host")
	}

	c.port = defaultPort
	if p := uu.Port(); p != "" {
		var port uint64
		port, err = strconv.ParseUint(p, 10, 16)
		if err != nil || port == 0 {
			return nil, fmt.Errorf("anytls: invalid port %q", p)
		}

		c.port = uint16(port)
	}

	query, err := url.ParseQuery(uu.RawQuery)
	if err != nil {
		return nil, fmt.Errorf("anytls: parsing parameters: %w", err)
	}

	for name, values := range query {
		if len(values) != 1 {
			return nil, fmt.Errorf("anytls: parameter %q: want one value, got %d", name, len(values))
		}

		err = c.setParam(name, values[0])
		if err != nil {
			return nil, fmt.Errorf("anytls: parameter %q: %w", name, err)
		}
	}

	if c.minVersion != 0 && c.maxVersion != 0 && c.minVersion > c.maxVersion {
		return nil, fmt.Errorf("anytls: min_version is above max_version")
	}

	c.key = strings.Join([]string{c.hostPort(), c.password, query.Encode()}, "\x00")

	return c, nil
}

// setParam sets the parameter name to value.
func (c *config) setParam(name, value string) (err error) {
	cc := &c.client
	switch name {
	case "sni", "server_name":
		c.serverName = value
	case "insecure":
		c.insecure, err = parseBool(value)
	case "alpn":
		c.alpn, err = parseList(value)
	case "min_version":
		c.minVersion, err = parseTLSVersion(value)
	case "max_version":
		c.maxVersion, err = parseTLSVersion(value)
	case "certificate_public_key_sha256":
		c.pins, err = parsePins(value)
	case "idle_session_check_interval":
		cc.IdleSessionCheckInterval, err = parseDuration(value)
	case "idle_session_timeout":
		cc.IdleSessionTimeout, err = parseDuration(value)
	case "min_idle_session":
		cc.MinIdleSession, err = parseCount(value)
	case "ensure_idle_session":
		cc.EnsureIdleSession, err = parseCount(value)
	case "min_idle_session_for_age":
		cc.MinIdleSessionForAge, err = parseCount(value)
	case "ensure_idle_session_create_rate":
		cc.EnsureIdleSessionCreateRate, err = parseCount(value)
	case "heartbeat":
		cc.Heartbeat, err = parseDuration(value)
	case "max_connection_lifetime":
		cc.MaxConnectionLifetime, err = parseDuration(value)
	case "connection_lifetime_jitter":
		cc.ConnectionLifetimeJitter, err = parseDuration(value)
	case "close_after_idle":
		cc.CloseAfterIdle, err = parseDuration(value)
	case "close_after_lifetime":
		cc.CloseAfterLifetime, err = parseDuration(value)
	case "client_metadata":
		cc.ClientMetadata = value
	default:
		return fmt.Errorf("unknown parameter")
	}

	return err
}

// parseBool parses a boolean parameter, where 1 is true as in the AnyTLS URI
// scheme.
func parseBool(s string) (ok bool, err error) {
	switch strings.ToLower(s) {
	case "1", "true":
		return true, nil
	case "", "0", "false":
		return false, nil
	default:
		return false, fmt.Errorf("invalid boolean %q", s)
	}
}

// parseDuration parses a duration such as "30s".  An empty value is zero,
// which leaves the AnyTLS default as an omitted field does in sing-box.
func parseDuration(s string) (d time.Duration, err error) {
	if s == "" {
		return 0, nil
	}

	d, err = time.ParseDuration(s)
	if err != nil {
		return 0, err
	}

	if d < 0 {
		return 0, fmt.Errorf("negative duration %s", s)
	}

	return d, nil
}

// parseCount parses a non-negative integer.
func parseCount(s string) (n int, err error) {
	n, err = strconv.Atoi(s)
	if err != nil {
		return 0, err
	}

	if n < 0 {
		return 0, fmt.Errorf("negative value %d", n)
	}

	return n, nil
}

// parseList parses a comma-separated list.
func parseList(s string) (l []string, err error) {
	for v := range strings.SplitSeq(s, ",") {
		v = strings.TrimSpace(v)
		if v == "" {
			return nil, fmt.Errorf("empty list element in %q", s)
		}

		l = append(l, v)
	}

	return l, nil
}

// parseTLSVersion parses a TLS version as sing-box does.
func parseTLSVersion(s string) (v uint16, err error) {
	switch s {
	case "1.0":
		return tls.VersionTLS10, nil
	case "1.1":
		return tls.VersionTLS11, nil
	case "1.2":
		return tls.VersionTLS12, nil
	case "1.3":
		return tls.VersionTLS13, nil
	default:
		return 0, fmt.Errorf("unknown tls version %q", s)
	}
}

// parsePins parses a comma-separated list of base64 SHA-256 hashes.  The
// standard and the URL-safe alphabets are both accepted, with or without
// padding.
func parsePins(s string) (pins [][]byte, err error) {
	values, err := parseList(s)
	if err != nil {
		return nil, err
	}

	for _, v := range values {
		var pin []byte
		pin, err = decodeBase64(v)
		if err != nil {
			return nil, fmt.Errorf("decoding %q: %w", v, err)
		}

		if len(pin) != sha256.Size {
			return nil, fmt.Errorf("%q is %d bytes, want %d", v, len(pin), sha256.Size)
		}

		pins = append(pins, pin)
	}

	return pins, nil
}

// decodeBase64 decodes s in any of the common base64 alphabets.
func decodeBase64(s string) (b []byte, err error) {
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	} {
		b, err = enc.DecodeString(s)
		if err == nil {
			return b, nil
		}
	}

	return nil, err
}

// verifyPins accepts the server when the public key of its leaf certificate
// has one of pins, as the sing-box certificate_public_key_sha256 option does.
func verifyPins(pins [][]byte, rawCerts [][]byte) (err error) {
	if len(rawCerts) == 0 {
		return fmt.Errorf("no server certificate")
	}

	leaf, err := x509.ParseCertificate(rawCerts[0])
	if err != nil {
		return fmt.Errorf("parsing leaf certificate: %w", err)
	}

	pubKey, err := x509.MarshalPKIXPublicKey(leaf.PublicKey)
	if err != nil {
		return fmt.Errorf("marshaling public key: %w", err)
	}

	sum := sha256.Sum256(pubKey)
	for _, pin := range pins {
		if bytes.Equal(pin, sum[:]) {
			return nil
		}
	}

	return fmt.Errorf("unrecognized remote public key: %s", base64.StdEncoding.EncodeToString(sum[:]))
}
