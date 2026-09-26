package migration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/egress"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

// Check the worker through the imported app and its journal-bound reverse
// channel. The app's /api/health responds locally and cannot establish this.
func (b *OfflineBackend) verifyMotionTarget(ctx context.Context, m *store.RuntimeMigration) error {
	id := egress.Identity{SandboxID: m.SandboxID, Generation: migrationChannelGeneration(m)}
	if b.Broker == nil || !b.Broker.authorizeMotion(ctx, id, m.Source.AppID.String) {
		return errors.New("Motion target worker capability is not authorized")
	}
	plain, err := b.Secrets.Open(m.Binding.TokenCiphertext, m.Binding.TokenNonce)
	if err != nil {
		return err
	}
	var credential credentials
	if err = json.Unmarshal(plain, &credential); err != nil {
		return err
	}
	port := int64(3000)
	if m.Source.WebPort.Valid {
		port = m.Source.WebPort.Int64
	}
	if port < 1 || port > 65535 {
		return errors.New("Motion target preview port is invalid")
	}
	host := fmt.Sprintf("%d-%s.%s", port, m.Binding.RuntimeID, m.Binding.Domain)
	if err = verifyMotionPreview(ctx, b.ProxyURL, host, credential.TrafficAccessToken); err != nil {
		return err
	}
	if !b.Broker.authorizeMotion(ctx, id, m.Source.AppID.String) {
		return errors.New("Motion target worker capability changed during verification")
	}
	return nil
}

func verifyMotionPreview(ctx context.Context, origin, host, trafficToken string) error {
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || trafficToken == "" {
		return errors.New("Motion target private ingress configuration is invalid")
	}
	transport := &http.Transport{
		DialContext:         (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
		TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 20 * time.Second,
		DisableCompression: true,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 25 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, path := range []string{"/api/status", "/api/projects"} {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(origin, "/")+path, nil)
		if err != nil {
			return errors.New("Motion target request is invalid")
		}
		req.Host = host
		req.Header.Set("cube-traffic-access-token", trafficToken)
		req.Header.Set("Accept", "application/json")
		response, err := client.Do(req)
		if err != nil {
			return errors.New("Motion target worker request failed")
		}
		const limit = 16 << 20
		body, readErr := io.ReadAll(io.LimitReader(response.Body, limit+1))
		response.Body.Close()
		if response.StatusCode != http.StatusOK || readErr != nil || len(body) > limit {
			return errors.New("Motion target worker response failed")
		}
		var value struct {
			Mode     string            `json:"mode"`
			Projects []json.RawMessage `json:"projects"`
		}
		if json.Unmarshal(body, &value) != nil ||
			(path == "/api/status" && value.Mode != "shared-workspace") ||
			(path == "/api/projects" && value.Projects == nil) {
			return errors.New("Motion target did not return worker status and projects")
		}
	}
	return nil
}
