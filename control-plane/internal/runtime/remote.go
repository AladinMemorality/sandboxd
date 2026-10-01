package runtime

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// RemoteConfig addresses one guest supervisor through a trusted private proxy
// or a TLS endpoint. Host overrides HTTP virtual-host routing, not TLS identity.
// Tokens must be generated independently per sandbox using crypto/rand. Never
// bake a live token into a reusable template or share it across sandboxes.
type RemoteConfig struct {
	BaseURL string
	Token   string
	Host    string
	// TrafficAccessToken is the per-sandbox private CubeProxy ingress token,
	// independent of the supervisor token. It is never the Cube API key.
	TrafficAccessToken string
}

// Clients are short-lived, but their connections should not be. The transport
// contains no sandbox credentials: Host and both tokens remain request-scoped.
// Bound idle connections across the worker origins and retain the existing
// redirect, proxy-discovery and timeout policies on each client.
var remoteTransport = &http.Transport{
	DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
	TLSHandshakeTimeout:   5 * time.Second,
	ResponseHeaderTimeout: 10 * time.Second,
	IdleConnTimeout:       90 * time.Second,
	MaxIdleConns:          256,
	MaxIdleConnsPerHost:   32,
}

// ValidateRemoteToken requires a hex or unpadded base64url representation of at
// least 32 random bytes. Encoding/length checks cannot prove randomness; the
// provisioning caller is responsible for cryptographic generation.
func ValidateRemoteToken(token string) error {
	if len(token) > 256 {
		return errors.New("runtimed token is too long")
	}
	if decoded, err := hex.DecodeString(token); err == nil && len(decoded) >= 32 {
		return nil
	}
	if decoded, err := base64.RawURLEncoding.Strict().DecodeString(token); err == nil && len(decoded) >= 32 && !strings.ContainsAny(token, "\r\n") {
		return nil
	}
	return errors.New("runtimed token must encode at least 32 random bytes as hex or unpadded base64url")
}

// NewRemoteClient retains the Unix client's task/status protocol over HTTP.
// Ordinary RPCs have a five-second timeout; event streams have no overall
// timeout and are cancelled by their context or closing the response body.
// Redirects and environment proxy discovery are disabled to avoid forwarding
// the guest's bearer token to another destination.
func NewRemoteClient(cfg RemoteConfig) (*Client, error) {
	if err := ValidateRemoteToken(cfg.Token); err != nil {
		return nil, err
	}
	if len(cfg.TrafficAccessToken) > 4096 || strings.IndexFunc(cfg.TrafficAccessToken, func(r rune) bool { return r < 0x21 || r > 0x7e }) >= 0 {
		return nil, errors.New("invalid Cube private ingress token")
	}
	u, err := url.Parse(cfg.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return nil, errors.New("runtimed BaseURL must be an http(s) origin without credentials, path, query or fragment")
	}
	if cfg.Host != "" {
		h, err := url.Parse("http://" + cfg.Host)
		if err != nil || h.Host != cfg.Host || h.Hostname() == "" || h.User != nil || h.Path != "" || h.RawQuery != "" || h.Fragment != "" || strings.ContainsAny(cfg.Host, " \t\r\n") {
			return nil, errors.New("invalid runtimed Host override")
		}
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	noRedirect := func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{
		remote: &cfg,
		http:   &http.Client{Timeout: 5 * time.Second, Transport: remoteTransport, CheckRedirect: noRedirect},
		stream: &http.Client{Transport: remoteTransport, CheckRedirect: noRedirect},
	}, nil
}
