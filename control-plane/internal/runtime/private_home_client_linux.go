package runtime

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"net/http"
	"time"
)

func (c *Client) privateHomeRequest(ctx context.Context, method, path string, manifest HomeManifest, body io.Reader, size int64) (*http.Response, func(), error) {
	if c.unavailable != nil {
		return nil, func() {}, c.unavailable
	}
	raw, e := CanonicalHomeManifest(manifest)
	if e != nil {
		return nil, func() {}, e
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
	if method == http.MethodPost {
		body = bytes.NewReader(raw)
		size = int64(len(raw))
	}
	if manifest.Version == 2 {
		path += "-v2"
		if method == http.MethodPut {
			var prefix [4]byte
			binary.BigEndian.PutUint32(prefix[:], uint32(len(raw)))
			body = io.MultiReader(bytes.NewReader(prefix[:]), bytes.NewReader(raw), body)
			size += int64(4 + len(raw))
		}
	}
	req, e := http.NewRequestWithContext(ctx, method, origin+path, body)
	if e != nil {
		cleanup()
		return nil, func() {}, e
	}
	req.ContentLength = size
	if method == http.MethodPut {
		if manifest.Version == 2 {
			req.Header.Set("Content-Type", "application/vnd.sandboxd.private-home-v2")
		} else {
			req.Header.Set("X-Home-Manifest", base64.RawURLEncoding.EncodeToString(raw))
			req.Header.Set("Content-Type", "application/zip")
		}
	} else {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.remote != nil {
		req.Host = c.remote.Host
		req.Header.Set("Authorization", "Bearer "+c.remote.Token)
		if c.remote.TrafficAccessToken != "" {
			req.Header.Set("cube-traffic-access-token", c.remote.TrafficAccessToken)
		}
	}
	resp, e := hc.Do(req)
	if e != nil {
		cleanup()
		return nil, func() {}, e
	}
	return resp, cleanup, nil
}
func (c *Client) ExportPrivateHome(ctx context.Context, manifest HomeManifest, dest io.Writer) error {
	resp, cleanup, e := c.privateHomeRequest(ctx, http.MethodPost, "/export/private-home", manifest, nil, 0)
	if e != nil {
		return e
	}
	defer cleanup()
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return &ResponseError{StatusCode: resp.StatusCode}
	}
	n, e := io.Copy(dest, io.LimitReader(resp.Body, MaxPrivateHomeBytes+1))
	if e != nil {
		return e
	}
	if n > MaxPrivateHomeBytes {
		return errors.New("private home response limit")
	}
	return nil
}
func (c *Client) ImportPrivateHome(ctx context.Context, manifest HomeManifest, source io.Reader, size int64) error {
	if size < 0 || size > MaxPrivateHomeBytes {
		return errors.New("private home request limit")
	}
	resp, cleanup, e := c.privateHomeRequest(ctx, http.MethodPut, "/import/private-home", manifest, io.LimitReader(source, size), size)
	if e != nil {
		return e
	}
	defer cleanup()
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return &ResponseError{StatusCode: resp.StatusCode}
	}
	return nil
}
