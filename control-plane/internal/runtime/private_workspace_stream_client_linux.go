package runtime

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"
)

func (c *Client) workspaceFileRequest(ctx context.Context, method, path string, body io.Reader, size int64) (*http.Response, func(), error) {
	if c.unavailable != nil {
		return nil, func() {}, c.unavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	hc := *c.stream
	var transport *http.Transport
	if t, ok := hc.Transport.(*http.Transport); ok {
		transport = t.Clone()
		transport.ResponseHeaderTimeout = 0
		hc.Transport = transport
	}
	cleanup := func() {
		cancel()
		if transport != nil {
			transport.CloseIdleConnections()
		}
	}
	origin := "http://runtimed"
	if c.remote != nil {
		origin = c.remote.BaseURL
	}
	req, err := http.NewRequestWithContext(ctx, method, origin+path, body)
	if err != nil {
		cleanup()
		return nil, func() {}, err
	}
	req.ContentLength = size
	req.Header.Set("Content-Type", "application/zip")
	if c.remote != nil {
		req.Host = c.remote.Host
		req.Header.Set("Authorization", "Bearer "+c.remote.Token)
		if c.remote.TrafficAccessToken != "" {
			req.Header.Set("cube-traffic-access-token", c.remote.TrafficAccessToken)
		}
	}
	resp, err := hc.Do(req)
	if err != nil {
		cleanup()
		return nil, func() {}, err
	}
	return resp, cleanup, nil
}
func (c *Client) ExportPrivateWorkspaceFile(ctx context.Context, dest io.Writer) error {
	resp, cleanup, err := c.workspaceFileRequest(ctx, http.MethodGet, "/export/private-workspace-v2", nil, 0)
	if err != nil {
		return err
	}
	defer cleanup()
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return &ResponseError{StatusCode: resp.StatusCode}
	}
	n, err := io.Copy(dest, io.LimitReader(resp.Body, MaxPrivateWorkspaceStreamBytes+1))
	if err != nil {
		return err
	}
	if n > MaxPrivateWorkspaceStreamBytes {
		return errors.New("private workspace response limit")
	}
	return nil
}
func (c *Client) ImportPrivateWorkspaceFile(ctx context.Context, source io.Reader, size int64) error {
	if size < 0 || size > MaxPrivateWorkspaceStreamBytes {
		return errors.New("private workspace request limit")
	}
	resp, cleanup, err := c.workspaceFileRequest(ctx, http.MethodPut, "/import/private-workspace-v2", io.LimitReader(source, size), size)
	if err != nil {
		return err
	}
	defer cleanup()
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return &ResponseError{StatusCode: resp.StatusCode}
	}
	return nil
}
