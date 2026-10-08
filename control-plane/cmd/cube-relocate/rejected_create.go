package main

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
	"io"
	"net/http"
)

// Root-only evidence transcribed from the native scheduler's request log.
// 130597 is a pre-allocation rejection, unlike transport failures/timeouts.
// It must identify this exact admission operation and original request.
type rejectedCreate struct {
	RelocationID, OperationToken, RequestID string
	HTTPStatus, FailureCode                 int
}

func validRejectedCreate(j store.CubeRelocation, t target, a cube.AdmissionRecord, p rejectedCreate) bool {
	return j.TargetWorker == "vps" && j == t.Relocation &&
		t.Runtime == nil && t.SupervisorToken != "" && a == t.Admission && a.Key == j.TargetKey && a.WorkerID == "vps" &&
		a.TemplateID == j.DestinationTemplate() && a.Operation == "create" && a.RuntimeID == "" && a.State == "pending" && a.Charged == 1 &&
		a.Token != "" && p.RelocationID == j.ID && p.OperationToken == a.Token && p.RequestID != "" && p.HTTPStatus == 500 && p.FailureCode == 130597
}
func noRelocationTarget(ctx context.Context, key string, j store.CubeRelocation, operation string) error {
	req, err := http.NewRequestWithContext(ctx, "GET", "http://127.0.0.1:20300/sandboxes", nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-API-Key", key)
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return errors.New("provider inventory unavailable")
	}
	// The pinned native API returns its complete inventory as one JSON array.
	var all []cube.Sandbox
	dec := json.NewDecoder(io.LimitReader(response.Body, 32<<20))
	if err = dec.Decode(&all); err != nil {
		return err
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return errors.New("incomplete provider inventory")
	}
	sourceFound := false
	for _, v := range all {
		if v.SandboxID == j.SourceRuntimeID {
			sourceFound = true
			continue
		}
		if v.Metadata["sandboxd_relocation_id"] == j.ID || v.Metadata["sandboxd_admission_operation"] == operation {
			return errors.New("provider target exists; adoption required")
		}
	}
	if !sourceFound {
		return errors.New("source missing from provider inventory")
	}
	return nil
}
