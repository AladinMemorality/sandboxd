package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/egress"
)

const cubeLocalProxy = "http://127.0.0.1:3032"

// These are routing hints, not an isolation boundary. The guest NIC remains
// deny-all, and the host independently authorizes every brokered destination.
func cubeProxyEnvironment() []string {
	return []string{"HTTP_PROXY=" + cubeLocalProxy, "HTTPS_PROXY=" + cubeLocalProxy,
		"http_proxy=" + cubeLocalProxy, "https_proxy=" + cubeLocalProxy,
		"NO_PROXY=localhost,127.0.0.1,::1", "no_proxy=localhost,127.0.0.1,::1", "NODE_USE_ENV_PROXY=1"}
}

func reverseEgressGuest(remote remoteControl) (*egress.Guest, error) {
	switch os.Getenv("RUNTIMED_CUBE_REVERSE_EGRESS") {
	case "", "0":
		return nil, nil
	case "1":
	default:
		return nil, errors.New("reverse egress image flag must be 0 or 1")
	}
	if os.Getenv("RUNTIMED_CUBE_GUEST") != "1" || remote.Address == "" {
		return nil, errors.New("reverse egress requires the authenticated Cube guest transport")
	}
	if err := remote.validate(); err != nil {
		return nil, err
	}
	expected := sha256.Sum256([]byte("Bearer " + remote.Token))
	guest, err := egress.NewGuest(egress.GuestOptions{Authenticate: func(r *http.Request) bool {
		supplied := sha256.Sum256([]byte(r.Header.Get("Authorization")))
		return len(r.Header.Values("Authorization")) == 1 && subtle.ConstantTimeCompare(supplied[:], expected[:]) == 1
	}})
	if err != nil {
		return nil, err
	}
	for _, entry := range cubeProxyEnvironment() {
		key, value, _ := strings.Cut(entry, "=")
		if err := os.Setenv(key, value); err != nil {
			guest.Close()
			return nil, err
		}
	}
	return guest, nil
}

// Only the local tenant can use this listener. The control channel is exposed
// separately on the authenticated supervisor listener, never on this mux.
func startCubeProxy(ctx context.Context, guest *egress.Guest) (func(), error) {
	if guest == nil {
		return func() {}, nil
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:3032")
	if err != nil {
		return nil, err
	}
	server := &http.Server{Handler: cubeProxyHandler(guest), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second,
		MaxHeaderBytes: 16 << 10, BaseContext: func(net.Listener) context.Context { return ctx }}
	stop := context.AfterFunc(ctx, func() { server.Close(); guest.Close() })
	go func() { _ = server.Serve(listener) }()
	return func() { stop(); server.Close(); guest.Close() }, nil
}

func cubeProxyHandler(guest *egress.Guest) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/__cube/model/", http.StripPrefix("/__cube/model", guest.ServiceHandler("model")))
	bridge := guest.ServiceHandler("bridge")
	mux.HandleFunc("/__cube/bridge", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/__cube/bridge" || r.URL.RawPath != "" || r.URL.RawQuery != "" {
			http.NotFound(w, r)
			return
		}
		copy := r.Clone(r.Context())
		copy.URL.Path = "/api/bridge"
		copy.RequestURI = "/api/bridge"
		bridge.ServeHTTP(w, copy)
	})
	mux.Handle("/", guest.ProxyHandler())
	proxy := guest.ProxyHandler()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodConnect || r.URL.IsAbs() {
			proxy.ServeHTTP(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// Bind the supervisor listener immediately, but delay applications that can
// fetch during startup until the host has established their reverse channel.
func superviseAfterCubeEgress(ctx context.Context, guest *egress.Guest, run func(context.Context)) {
	if guest != nil {
		if err := guest.WaitReady(ctx); err != nil {
			return
		}
	}
	if ctx.Err() == nil {
		run(ctx)
	}
}
