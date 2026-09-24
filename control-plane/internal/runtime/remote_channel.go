package runtime

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/gorilla/websocket"
)

// OpenEgressChannel initiates the reverse channel through the same trusted
// private ingress as supervisor RPCs. No guest-supplied destination, ambient
// proxy or redirect can receive either per-guest credential.
func (c *Client) OpenEgressChannel(ctx context.Context) (*websocket.Conn, error) {
	if c.unavailable != nil {
		return nil, c.unavailable
	}
	if c.remote == nil {
		return nil, errors.New("reverse egress requires an authenticated remote supervisor")
	}
	u, err := url.Parse(c.remote.BaseURL)
	if err != nil {
		return nil, errors.New("invalid remote supervisor origin")
	}
	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else {
		u.Scheme = "ws"
	}
	u.Path = "/egress/channel"
	headers := http.Header{"Authorization": {"Bearer " + c.remote.Token}}
	if c.remote.Host != "" {
		headers.Set("Host", c.remote.Host)
	}
	if c.remote.TrafficAccessToken != "" {
		headers.Set("cube-traffic-access-token", c.remote.TrafficAccessToken)
	}
	dialer := websocket.Dialer{
		NetDialContext:   (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		HandshakeTimeout: 5 * time.Second,
		ReadBufferSize:   4096, WriteBufferSize: 4096,
	}
	conn, response, err := dialer.DialContext(ctx, u.String(), headers)
	if err != nil && response != nil && response.Body != nil {
		response.Body.Close()
	}
	if err != nil {
		return nil, errors.New("authenticated reverse egress channel unavailable")
	}
	return conn, nil
}
