package api

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
)

// This runs only after the preview's owner/cookie/origin checks. Navigation
// retries remain GETs at the same stable URL; bodies and failed writes are never
// replayed. No request URL, credential or upstream message enters the document.
func writeCubePreviewAdmission(w http.ResponseWriter, r *http.Request, err error) bool {
	navigation := r.Method == http.MethodGet && (r.Header.Get("Sec-Fetch-Mode") == "navigate" ||
		(r.Header.Get("Sec-Fetch-Mode") == "" && strings.Contains(r.Header.Get("Accept"), "text/html")))
	if !errors.Is(err, cube.ErrCapacityUnavailable) || !navigation {
		return writeCubeAdmissionError(w, err)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Retry-After", "5")
	w.Header().Set("Vary", "Accept, Sec-Fetch-Mode")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; base-uri 'none'; form-action 'none'")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = io.WriteString(w, `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta http-equiv="refresh" content="5"><title>App slots are busy</title></head><body><main><h1>App slots are busy</h1><p>Your saved app is safe. This page retries every five seconds while a slot becomes available.</p><p><a href="">Retry now</a></p></main></body></html>`)
	return true
}
