// Package appenv turns an app's stored config into the environment its
// sandbox container starts with.
//
// The config store (internal/store.AppConfig, written through
// /v1/apps/{id}/config) existed with nowhere to go: values were sealed at
// rest and never reached a process. This is the delivery. Every entry whose
// access policy allows the runtime becomes one KEY=VALUE in the container
// environment when the container is created or recreated, which is the only
// moment Docker takes an environment. A changed value therefore needs a
// recreate, which the API offers as POST /v1/sandboxes/{id}/recreate.
//
// Values are decrypted here and handed to docker run as --env flags. They
// are visible to every process in the container, the coding agent included,
// and in `docker inspect` on the host; that is the contract of "runtime
// access", and the reason the default policy stays control_plane_only.
package appenv

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"sort"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/secrets"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

// envName is what a shell and every runtime accept as a variable name. The
// config API allows a wider key (any printable string); a key that is not a
// variable name is simply not delivered rather than breaking docker run.
var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,255}$`)

// runtimeVisible reports whether a policy lets the app's runtime see a value.
func runtimeVisible(policy string) bool {
	return policy == "runtime_access" || policy == "both"
}

// Format builds KEY=VALUE pairs from config rows, decrypting sensitive ones
// with open. Rows the runtime may not see, rows whose key is not a variable
// name, and rows that fail to decrypt are skipped (the last is logged by the
// caller through the returned count of skipped rows). Output is sorted by
// key so a recreate is deterministic.
func Format(rows []*store.AppConfig, open func(ciphertext, nonce []byte) ([]byte, error)) (env []string, skipped int) {
	for _, c := range rows {
		if !runtimeVisible(c.AccessPolicy) || !envName.MatchString(c.Key) {
			continue
		}
		var value string
		switch {
		case c.Sensitive:
			if open == nil || len(c.ValueCiphertext) == 0 {
				skipped++
				continue
			}
			pt, err := open(c.ValueCiphertext, c.ValueNonce)
			if err != nil {
				skipped++
				continue
			}
			value = string(pt)
		case c.ValuePlaintext.Valid:
			value = c.ValuePlaintext.String
		default:
			continue
		}
		env = append(env, c.Key+"="+value)
	}
	sort.Strings(env)
	return env, skipped
}

// For loads the app's config and formats it. A missing app id (a sandbox
// created outside the app model) yields nothing.
func For(ctx context.Context, st *store.Store, cipher *secrets.Cipher, appID string) ([]string, error) {
	if appID == "" {
		return nil, nil
	}
	rows, err := st.ListAppConfig(ctx, appID)
	if err != nil {
		return nil, fmt.Errorf("list app config: %w", err)
	}
	var open func(ciphertext, nonce []byte) ([]byte, error)
	if cipher != nil {
		open = cipher.Open
	}
	env, skipped := Format(rows, open)
	if skipped > 0 {
		return env, fmt.Errorf("%d config value(s) could not be decrypted and were not delivered", skipped)
	}
	return env, nil
}

// Best is For with the error logged instead of returned: a container that
// starts without one value beats a container that does not start.
func Best(ctx context.Context, st *store.Store, cipher *secrets.Cipher, appID string, log *slog.Logger) []string {
	env, err := For(ctx, st, cipher, appID)
	if err != nil && log != nil {
		log.Warn("app env: partial delivery", "app", appID, "err", err.Error())
	}
	return env
}
