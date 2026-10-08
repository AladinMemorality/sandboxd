package cube

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type FleetStore interface {
	AdmissionPartition(string) (AdmissionStore, error)
	AdmissionLookup(context.Context, string) (AdmissionRecord, error)
	AdmissionLookupKey(context.Context, string) (AdmissionRecord, error)
}
type FleetWorkerConfig struct {
	ID        string          `json:"id"`
	ProxyURL  string          `json:"proxy_url"`
	Draining  bool            `json:"draining"`
	Admission AdmissionConfig `json:"admission"`
}
type fleet struct {
	store   FleetStore
	workers map[string]*Client
	proxies map[string]string
	order   []string
}

// ConfigurePlacement uses only an operator-configured private CubeMaster
// endpoint. The public sandbox detail omits host_id and is not placement proof.
func (c *Client) ConfigurePlacement(origin, instanceType string) error {
	u, err := trustedOrigin(origin)
	if err != nil || validateID(instanceType) != nil {
		return errors.New("invalid Cube placement observer configuration")
	}
	c.observation = func(ctx context.Context, id string) (*Sandbox, error) {
		if validateID(id) != nil {
			return nil, ErrAdmissionUnknown
		}
		target := *u
		target.Path = "/cube/sandbox/info"
		target.RawQuery = url.Values{"sandbox_id": {id}, "instance_type": {instanceType}}.Encode()
		bounded, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(bounded, http.MethodGet, target.String(), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("X-Caller", masterControlCaller)
		response, err := c.http.Do(req)
		if err != nil {
			return nil, errors.New("Cube placement observation unavailable")
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			return nil, errors.New("Cube placement observation rejected")
		}
		raw, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
		if err != nil || len(raw) > maxResponseBytes {
			return nil, errors.New("invalid Cube placement observation")
		}
		return decodeMasterObservation(raw, id)
	}
	c.inventoryNode = func(ctx context.Context, node string) ([]Sandbox, error) { return c.masterNodeInventory(ctx, u, node) }
	c.resumeObserved = c.nativeResume(u, instanceType)
	c.placement = func(ctx context.Context, id, node string) error {
		value, err := c.observation(ctx, id)
		if err != nil {
			return err
		}
		if value.ClientID != node {
			return errors.New("Cube runtime is not on the reserved worker")
		}
		return nil
	}
	return nil
}

func trustedOrigin(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") || u.RawPath != "" || strings.ContainsAny(u.Host, "\\ \t\r\n") {
		return nil, errors.New("invalid trusted origin")
	}
	return u, nil
}

// ConfigureFleet is startup-only. All workers use the same Cube control plane
// and reviewed template IDs. Every child retains the complete durable admission
// protocol; failure after reservation never falls through to another worker.
func (c *Client) ConfigureFleet(ctx context.Context, db FleetStore, workers []FleetWorkerConfig) error {
	if db == nil || c.placement == nil || len(workers) < 2 || len(workers) > 16 {
		return errors.New("fleet requires store, placement verifier and bounded workers")
	}
	f := &fleet{store: db, workers: map[string]*Client{}, proxies: map[string]string{}}
	nodes := map[string]bool{}
	ids := map[string]bool{}
	for _, w := range workers {
		if validateID(w.ID) != nil || ids[w.ID] || w.Admission.NodeID == "" || nodes[w.Admission.NodeID] {
			return errors.New("invalid or duplicate fleet worker/node")
		}
		if _, err := trustedOrigin(w.ProxyURL); err != nil {
			return err
		}
		if err := w.Admission.RequireStorageGuard(); err != nil {
			return err
		}
		if err := w.Admission.validate(); err != nil {
			return err
		}
		ids[w.ID] = true
		nodes[w.Admission.NodeID] = true
	}
	if !ids["vps"] {
		return errors.New("fleet must preserve existing VPS partition")
	}
	for _, w := range workers {
		partition, err := db.AdmissionPartition(w.ID)
		if err != nil {
			return err
		}
		child := *c
		child.fleet = nil
		child.admission = nil
		if err = child.ConfigureAdmission(ctx, partition, w.Admission); err != nil {
			return err
		}
		f.workers[w.ID] = &child
		f.proxies[w.ID] = w.ProxyURL
		if !w.Draining {
			f.order = append(f.order, w.ID)
		}
	}
	if f.workers["vps"] == nil {
		return errors.New("fleet must preserve existing VPS partition")
	}
	c.fleet = f
	c.admission = f.workers["vps"].admission
	return nil
}

func (f *fleet) runtime(ctx context.Context, id string) (*Client, error) {
	a, err := f.store.AdmissionLookup(ctx, id)
	if err != nil {
		if errors.Is(err, ErrRuntimeUnavailable) {
			return nil, err
		}
		return nil, ErrAdmissionUnknown
	}
	w := f.workers[a.WorkerID]
	if w == nil {
		return nil, ErrAdmissionUnknown
	}
	return w, nil
}
func (f *fleet) create(ctx context.Context, in CreateRequest) (*Sandbox, error) {
	if len(in.DistributionScope) != 0 {
		return nil, errors.New("fleet placement is selected by durable admission")
	}
	for _, id := range f.order {
		result, err := f.workers[id].Create(ctx, in)
		if err == nil {
			return result, nil
		}
		if !errors.Is(err, ErrCapacityUnavailable) {
			return nil, err
		}
	}
	return nil, ErrCapacityUnavailable
}

// ProxyOrigin returns a trusted configured worker origin, never guest metadata.
func (c *Client) ProxyOrigin(ctx context.Context, id, fallback string) (string, error) {
	if c.fleet == nil {
		return fallback, nil
	}
	a, err := c.fleet.store.AdmissionLookup(ctx, id)
	if err != nil {
		return "", ErrAdmissionUnknown
	}
	origin := c.fleet.proxies[a.WorkerID]
	if origin == "" {
		return "", ErrAdmissionUnknown
	}
	return origin, nil
}
