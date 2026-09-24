package api

import (
	"encoding/json"
	"io"
	"net/http"
)

func readRecreateRequest(w http.ResponseWriter, r *http.Request) (bool, bool) {
	var request map[string]any
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	if err := decoder.Decode(&request); (err != nil && err != io.EOF) || (err == nil && request == nil) {
		writeV1Err(w, 400, "invalid_request", "expected an optional reload_manifest boolean")
		return false, false
	}
	reload := false
	for key, value := range request {
		flag, ok := value.(bool)
		if key != "reload_manifest" || !ok {
			writeV1Err(w, 400, "invalid_request", "expected an optional reload_manifest boolean")
			return false, false
		}
		reload = flag
	}
	if decoder.Decode(new(any)) != io.EOF {
		writeV1Err(w, 400, "invalid_request", "expected a single request object")
		return false, false
	}
	return reload, true
}
