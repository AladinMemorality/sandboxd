package cube

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	c, err := New(Config{APIURL: s.URL, APIKey: "test-secret"})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func validCreate() CreateRequest {
	return CreateRequest{TemplateID: "tpl-trusted", Network: &NetworkPolicy{DenyOut: []string{"0.0.0.0/0"}}}
}

// Wire fixtures follow CubeAPI/src/models/mod.rs and routes.rs. In particular
// create/connect use uppercase ID; snapshot deletion returns READY, not 204;
// and Connect requires a JSON object even when no timeout is supplied.
func TestLifecycleWireContract(t *testing.T) {
	var seen []string
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path)
		if r.Header.Get("X-API-Key") != "test-secret" {
			t.Error("missing key")
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("unexpected bearer token")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "POST /sandboxes":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["templateID"] != "tpl-trusted" || body["timeout"] != float64(600) {
				t.Errorf("bad create fields: %v", body)
			}
			if body["envVars"].(map[string]any)["APP_ENV"] != "test" || body["metadata"].(map[string]any)["project"] != "project-one" {
				t.Error("missing env/metadata")
			}
			lc := body["lifecycle"].(map[string]any)
			if lc["onTimeout"] != "pause" || lc["autoResume"] != true {
				t.Error("destructive lifecycle default")
			}
			network := body["network"].(map[string]any)
			if network["allowPublicTraffic"] != false || body["allow_internet_access"] != false {
				t.Error("missing explicit policy flags")
			}
			if _, ok := network["allowOut"].([]any); !ok {
				t.Error("expected explicit empty array")
			}
			w.WriteHeader(201)
			fmt.Fprint(w, `{"sandboxID":"abc-123","templateID":"tpl-trusted","trafficAccessToken":"private-ingress"}`)
		case "GET /sandboxes/abc-123":
			fmt.Fprint(w, `{"sandboxID":"abc-123","templateID":"tpl-trusted","state":"paused","cpuCount":2,"memoryMB":2048}`)
		case "POST /sandboxes/abc-123/connect":
			b, _ := io.ReadAll(r.Body)
			if string(b) != "{}" {
				t.Errorf("connect body %q", b)
			}
			fmt.Fprint(w, `{"sandboxID":"abc-123","templateID":"tpl-trusted","domain":"cube.invalid"}`)
		case "POST /sandboxes/abc-123/pause", "DELETE /sandboxes/abc-123":
			w.WriteHeader(204)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	})
	in := validCreate()
	in.TimeoutSeconds = 600
	in.EnvVars = map[string]string{"APP_ENV": "test"}
	in.Metadata = map[string]string{"project": "project-one"}
	out, err := c.Create(context.Background(), in)
	if err != nil || out.TrafficAccessToken != "private-ingress" {
		t.Fatalf("create: %#v %v", out, err)
	}
	if in.Lifecycle != nil || in.Network.AllowOut != nil {
		t.Fatal("mutated caller request")
	}
	out, err = c.Get(context.Background(), out.SandboxID)
	if err != nil || out.State != "paused" || out.MemoryMB != 2048 {
		t.Fatalf("get: %#v %v", out, err)
	}
	if _, err = c.Connect(context.Background(), "abc-123", ConnectRequest{}); err != nil {
		t.Fatal(err)
	}
	if err = c.Pause(context.Background(), "abc-123"); err != nil {
		t.Fatal(err)
	}
	if err = c.Delete(context.Background(), "abc-123"); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 5 {
		t.Fatalf("request count %d", len(seen))
	}
}

func TestSnapshotWireContract(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /sandboxes/abc-123/snapshots":
			var body SnapshotRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.Name != "private-checkpoint" || body.Backend != "xfs" {
				t.Error("bad snapshot request")
			}
			w.WriteHeader(201)
			fmt.Fprint(w, `{"snapshotID":"snap-one","names":["private-checkpoint"],"backend":"xfs"}`)
		case "POST /sandboxes":
			var body CreateRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.TemplateID != "snap-one" || body.Lifecycle.OnTimeout != "pause" {
				t.Error("bad restore template/lifecycle")
			}
			w.WriteHeader(201)
			fmt.Fprint(w, `{"sandboxID":"new-sandbox","templateID":"snap-one"}`)
		case "DELETE /templates/snap-one":
			fmt.Fprint(w, `{"templateID":"snap-one","operationID":"operation-one","status":"READY"}`)
		default:
			t.Errorf("bad snapshot route: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	})
	snap, err := c.CreateSnapshot(context.Background(), "abc-123", SnapshotRequest{Name: "private-checkpoint", Backend: "xfs"})
	if err != nil || snap.SnapshotID != "snap-one" {
		t.Fatalf("snapshot: %#v %v", snap, err)
	}
	if _, err = c.CreateFromSnapshot(context.Background(), snap.SnapshotID, validCreate()); err != nil {
		t.Fatal(err)
	}
	if err = c.DeleteSnapshot(context.Background(), snap.SnapshotID); err != nil {
		t.Fatal(err)
	}
}

func TestRejectUnsafeConfiguration(t *testing.T) {
	for _, endpoint := range []string{"", "file:///etc/passwd", "http://user:secret@localhost", "http://localhost?key=secret", "http://localhost#fragment", "http://localhost/a/../b", "http://localhost/%2e%2e", "http://localhost:99999", "http://localhost:0", "http://localhost/a%2Fb"} {
		if _, err := New(Config{APIURL: endpoint, APIKey: "key"}); err == nil {
			t.Errorf("accepted URL %q", endpoint)
		}
	}
	for _, key := range []string{"", "   ", "key\r\nInjected: secret", "key\x00", "key\t", "héllo"} {
		if _, err := New(Config{APIURL: "http://127.0.0.1:3000", APIKey: key}); err == nil {
			t.Errorf("accepted invalid key")
		}
	}
}

func TestRejectUnsafeRequestBeforeNetwork(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) { t.Error("unsafe request reached upstream") })
	for _, id := range []string{"", "../other", "abc/../other", "http://host", "abc?x=y", "abc%2fother", "abc\n", strings.Repeat("a", 129)} {
		if _, err := c.Get(context.Background(), id); err == nil {
			t.Errorf("accepted identifier %q", id)
		}
	}
	for _, mutate := range []func(*CreateRequest){
		func(r *CreateRequest) { r.Network = nil },
		func(r *CreateRequest) { r.Lifecycle = &Lifecycle{OnTimeout: "kill"} },
		func(r *CreateRequest) { r.Metadata = map[string]string{"host-mount": "/etc:/guest"} },
		func(r *CreateRequest) { r.Metadata = map[string]string{"HOST-MOUNT": "/etc:/guest"} },
		func(r *CreateRequest) { r.EnvVars = map[string]string{"A=B": "secret"} },
		func(r *CreateRequest) { r.EnvVars = map[string]string{"KEY": "secret\x00"} },
		func(r *CreateRequest) { r.EnvVars = map[string]string{"KEY": strings.Repeat("x", maxRequestBytes)} },
		func(r *CreateRequest) { r.TimeoutSeconds = -1 },
		func(r *CreateRequest) { r.Backend = "http://other" },
	} {
		in := validCreate()
		mutate(&in)
		if _, err := c.Create(context.Background(), in); err == nil {
			t.Error("accepted unsafe create")
		}
	}
}

func TestRedirectNeverForwardsCredentials(t *testing.T) {
	var calls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer target.Close()
	for _, status := range []int{301, 302, 303, 307, 308} {
		source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", target.URL)
			w.WriteHeader(status)
		}))
		provided := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return nil }}
		c, err := New(Config{APIURL: source.URL, APIKey: "do-not-leak", HTTPClient: provided})
		if err != nil {
			t.Fatal(err)
		}
		_, err = c.Get(context.Background(), "abc")
		var apiErr *APIError
		if !errors.As(err, &apiErr) || apiErr.StatusCode != status {
			t.Errorf("redirect result %v", err)
		}
		source.Close()
	}
	if calls.Load() != 0 {
		t.Fatal("redirect target received request")
	}
}

func TestHTTPFailureRedactedAndNotRetried(t *testing.T) {
	var calls atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "5")
		w.WriteHeader(503)
		fmt.Fprint(w, `{"message":"test-secret, KEY=customer-secret, http://internal"}`)
	})
	_, err := c.Create(context.Background(), validCreate())
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 503 || apiErr.RetryAfterSeconds != 5 {
		t.Fatalf("unusable typed error: %v", err)
	}
	if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "internal") {
		t.Fatalf("error leaked upstream details: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatal("mutation retried")
	}
}

func TestContextAndClientTimeout(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
		case <-time.After(time.Second):
		}
	}))
	defer s.Close()
	c, err := New(Config{APIURL: s.URL, APIKey: "key"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err = c.Connect(ctx, "abc", ConnectRequest{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lost deadline: %v", err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	if _, err = c.Get(ctx, "abc"); !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation: %v", err)
	}
	c, err = New(Config{APIURL: s.URL, APIKey: "key", HTTPClient: &http.Client{Timeout: 20 * time.Millisecond}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Get(context.Background(), "abc"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lost client timeout: %v", err)
	}
}

func TestResponseBoundsAndValidation(t *testing.T) {
	for _, data := range []string{
		strings.Repeat("x", maxResponseBytes+1),
		`{"sandboxID":"abc","templateID":"tpl"} {"extra":true}`,
		`{"sandboxID":"other","templateID":"tpl"}`,
		`{"sandboxID":"../escape","templateID":"tpl"}`,
		`{"sandboxID":"abc","templateID":"tpl","state":"unknown"}`,
		`{"sandboxID":"abc","envdAccessToken":"private"}`,
		`{"secret":"private`,
	} {
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, data) })
		if _, err := c.Get(context.Background(), "abc"); err == nil {
			t.Error("invalid response accepted")
		} else if strings.Contains(err.Error(), "private") {
			t.Error("response leaked into error")
		}
	}
}

func TestConfiguredBasePath(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/private-api/sandboxes/abc" {
			t.Errorf("bad path %s", r.URL.Path)
		}
		fmt.Fprint(w, `{"sandboxID":"abc","templateID":"tpl"}`)
	}))
	defer s.Close()
	c, err := New(Config{APIURL: s.URL + "/private-api/", APIKey: "key"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Get(context.Background(), "abc"); err != nil {
		t.Fatal(err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestTransportErrorRedactionAndOperationDeadlines(t *testing.T) {
	var deadline time.Duration
	c, err := New(Config{APIURL: "http://private-control:3000", APIKey: "private-key", HTTPClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		d, ok := r.Context().Deadline()
		if !ok {
			t.Error("missing operation deadline")
		}
		deadline = time.Until(d)
		return nil, errors.New("http://private-control:3000 private-key customer-secret")
	})}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		call   func() error
		budget time.Duration
	}{
		{func() error { _, err := c.Get(context.Background(), "abc"); return err }, standardTimeout},
		{func() error { return c.Pause(context.Background(), "abc") }, lifecycleTimeout},
		{func() error { _, err := c.CreateSnapshot(context.Background(), "abc", SnapshotRequest{}); return err }, snapshotTimeout},
	} {
		err := tc.call()
		if err == nil || strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "customer") {
			t.Fatalf("unsafe transport error: %v", err)
		}
		if deadline > tc.budget || deadline < tc.budget-time.Second {
			t.Fatalf("deadline %v, expected %v", deadline, tc.budget)
		}
	}
}

func TestBodyReadTimeout(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
		case <-time.After(time.Second):
		}
	}))
	defer s.Close()
	c, err := New(Config{APIURL: s.URL, APIKey: "key", HTTPClient: &http.Client{Timeout: 20 * time.Millisecond}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Get(context.Background(), "abc"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lost body deadline: %v", err)
	}
}
