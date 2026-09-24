package main

import (
	"encoding/json"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
)

func (a *app) handleAppConfig(w http.ResponseWriter, r *http.Request) {
	if a.requestRestart == nil {
		http.Error(w, "supervisor restart unavailable", 503)
		return
	}
	var req runtime.AppConfigRequest
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&req) != nil || runtime.ValidateAppConfig(req) != nil {
		http.Error(w, "invalid config", 400)
		return
	}
	for key, value := range req.Env {
		if !runtime.ValidAppConfigKey(key) || len(value) > 32768 || strings.ContainsRune(value, 0) {
			http.Error(w, "invalid config", 400)
			return
		}
	}
	a.taskMu.Lock()
	defer a.taskMu.Unlock()
	if req.Revision == a.appConfigRevision {
		w.WriteHeader(204)
		return
	}
	if a.restartPending || (a.task != nil && !a.task.isDone()) {
		http.Error(w, "supervisor busy", 409)
		return
	}
	a.nextAppConfig = &req
	a.restartPending = true
	w.WriteHeader(202)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	go func() { time.Sleep(100 * time.Millisecond); a.requestRestart() }()
}

// Replacement starts from the current process environment, removes all previous
// app-owned keys, then installs the new set. It is used only for syscall.Exec;
// the old process never reports a revision it has not actually booted with.
func replacementAppEnv(current []string, req *runtime.AppConfigRequest) []string {
	if req == nil {
		return current
	}
	oldKeys := map[string]bool{}
	for _, entry := range current {
		key, value, ok := strings.Cut(entry, "=")
		if ok && key == "RUNTIMED_APP_ENV_KEYS" {
			for _, old := range strings.Split(value, ",") {
				if runtime.ValidAppConfigKey(old) {
					oldKeys[old] = true
				}
			}
		}
	}
	env := map[string]string{}
	for _, entry := range current {
		key, value, ok := strings.Cut(entry, "=")
		if ok && !oldKeys[key] && key != "RUNTIMED_APP_ENV_KEYS" && key != "RUNTIMED_APP_CONFIG_REVISION" {
			env[key] = value
		}
	}
	keys := []string{}
	for key, value := range req.Env {
		env[key] = value
		keys = append(keys, key)
	}
	sort.Strings(keys)
	env["RUNTIMED_APP_ENV_KEYS"] = strings.Join(keys, ",")
	env["RUNTIMED_APP_CONFIG_REVISION"] = req.Revision
	out := make([]string, 0, len(env))
	for key, value := range env {
		out = append(out, key+"="+value)
	}
	sort.Strings(out)
	return out
}
func (a *app) restartEnvironment() []string {
	a.taskMu.Lock()
	defer a.taskMu.Unlock()
	return replacementAppEnv(os.Environ(), a.nextAppConfig)
}
