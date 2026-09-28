package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

type previewGatewayWorkerKey struct{}

// PreviewGatewayRequest carries request metadata only. App bodies and responses
// stay on the app's worker; authorization and wake decisions remain authoritative.
type PreviewGatewayRequest struct {
	Host    string      `json:"host"`
	Method  string      `json:"method"`
	URI     string      `json:"uri"`
	Headers http.Header `json:"headers"`
}

type PreviewGatewayRoute struct {
	Host    string `json:"host"`
	Token   string `json:"token"`
	Private bool   `json:"private"`
	Passive bool   `json:"passive"`
}

func ParsePreviewGatewayKeys(raw string) map[string]string {
	var keys map[string]string
	if json.Unmarshal([]byte(raw), &keys) != nil {
		return nil
	}
	for worker, key := range keys {
		if worker == "" || len(key) < 32 {
			return nil
		}
	}
	return keys
}

func (s *Server) handlePreviewGateway(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	worker := r.Header.Get("X-Preview-Worker")
	key := s.PreviewGatewayKeys[worker]
	if len(key) < 32 || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+key)) != 1 {
		writeErr(w, 401, "preview gateway unauthorized")
		return
	}
	var in PreviewGatewayRequest
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32768))
	d.DisallowUnknownFields()
	if d.Decode(&in) != nil || in.Host == "" || strings.ContainsAny(in.Host, "/\\\r\n\t ") {
		writeErr(w, 400, "invalid preview request")
		return
	}
	u, err := url.ParseRequestURI(in.URI)
	if err != nil || u.IsAbs() || u.Host != "" || !strings.HasPrefix(in.URI, "/") || strings.HasPrefix(in.URI, "//") {
		writeErr(w, 400, "invalid preview path")
		return
	}
	switch in.Method {
	case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS":
	default:
		writeErr(w, 400, "invalid preview method")
		return
	}
	req := &http.Request{Method: in.Method, URL: u, Host: in.Host, Header: in.Headers, Body: http.NoBody}
	if req.Header == nil {
		req.Header = make(http.Header)
	}
	req = req.WithContext(context.WithValue(r.Context(), previewGatewayWorkerKey{}, worker))
	if !s.TryServeCubePreview(w, req) {
		http.NotFound(w, r)
	}
}
