package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/recovery"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/secrets"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const rejectedRequestID = "a36092b6-69ee-4add-ad3a-00e485ee8b39"
const rejectedLogSHA = "f99266118b1de556a6e6bdf91d07d017bff1fd60981ddbcdd849b0f80c537cb6"

// This is deliberately incident-specific. Generic HTTP 500 remains ambiguous.
// Only the exact operator-reviewed, terminal scheduler rejection permits one
// new attempt. A durable exclusive marker forbids repeating that attempt.
func validateRejection(data []byte, j *store.CubeRecoveryJournal) error {
	if fmt.Sprintf("%x", sha256.Sum256(data)) != rejectedLogSHA {
		return errors.New("reviewed master log changed")
	}
	return validateRejectionRecords(data, j)
}

func validateRejectionRecords(data []byte, j *store.CubeRecoveryJournal) error {
	request, rejected, disk := 0, 0, 0
	for _, line := range bytes.Split(data, []byte("\n")) {
		var row struct {
			RequestID string `json:"RequestId"`
			Content   string `json:"LogContent"`
		}
		if json.Unmarshal(line, &row) != nil || row.RequestID != rejectedRequestID {
			continue
		}
		if strings.HasPrefix(row.Content, "CreateSandbox:") {
			var in struct {
				RequestID string            `json:"requestID"`
				Labels    map[string]string `json:"labels"`
			}
			if json.Unmarshal([]byte(strings.TrimPrefix(row.Content, "CreateSandbox:")), &in) != nil || in.RequestID != rejectedRequestID {
				return errors.New("master request malformed")
			}
			for k, v := range map[string]string{"sandboxd_recovery_id": j.ID, "sandboxd_admission_operation": j.OperationToken, "sandboxd_id": j.SandboxID, "sandboxd_app_id": j.AppID, "cube.master.appsnapshot.template.id": j.Target.TemplateID} {
				if in.Labels[k] != v {
					return errors.New("master request does not bind this journal")
				}
			}
			request++
		}
		if row.Content == "Filter/disk select:10.0.2.15, StorageDiskUsagePer:66.68391338262485, DiskUsageMaxPercent:65" {
			disk++
		}
		if strings.HasPrefix(row.Content, "CreateSandbox_rsp fail:") {
			var out struct {
				RequestID string
				Ret       struct {
					Code    int    `json:"ret_code"`
					Message string `json:"ret_msg"`
				} `json:"ret"`
			}
			if json.Unmarshal([]byte(strings.TrimPrefix(row.Content, "CreateSandbox_rsp fail:")), &out) != nil || out.RequestID != rejectedRequestID || out.Ret.Code != 130597 || out.Ret.Message != "no more resource" {
				return errors.New("not the reviewed terminal pre-allocation rejection")
			}
			rejected++
		}
	}
	if request != 1 || rejected != 1 || disk != 1 {
		return errors.New("unique request, disk refusal and terminal rejection required")
	}
	return nil
}

func exclusiveReceipt(name string, value any) {
	b, e := json.Marshal(value)
	must(e)
	f, e := os.OpenFile(stage+"/"+name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	must(e)
	_, e = f.Write(b)
	must(e)
	must(f.Sync())
	must(f.Close())
	d, e := os.Open(stage)
	must(e)
	must(d.Sync())
	must(d.Close())
}

func reconcileRejected(ctx context.Context, session *recovery.Session, cfg config, key *secrets.Cipher, hashes map[string]string, adoptOnly bool) {
	j, e := session.Journal(ctx, journalID)
	must(e)
	if j.ID != journalID || j.SandboxID != sandbox || j.AppID != appID || j.Old.RuntimeID != oldRuntime || j.Target.TemplateID != template || j.TaskCount != 1 || (j.Phase != "creating" && !(adoptOnly && j.Phase == "created")) {
		panic("exact unfinished incident required")
	}
	for role, h := range hashes {
		if j.Artifacts[role] != h {
			panic("frozen recovery artifact changed")
		}
	}
	var remote cube.Sandbox
	if adoptOnly {
		readJSON(stage+"/retry-response.PRIVATE.json", &remote)
	} else {
		if j.Target.RuntimeID != "" {
			panic("target already acknowledged; adopt only")
		}
		log, e := os.ReadFile(stage + "/master-create-tail.PRIVATE.log")
		must(e)
		must(validateRejection(log, j))
		// The worker and host disks grew online. Confirm current measured usage has
		// returned below the existing native 65% cutoff before sending any request.
		worker(ctx, `python3 -c 'import os; s=os.statvfs("/data"); assert s.f_blocks*s.f_frsize>500*1024**3; assert 100*(s.f_blocks-s.f_bfree)/s.f_blocks<63; print("native storage admission headroom verified")'`)
		hc := &http.Client{Timeout: 120 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		req, e := http.NewRequestWithContext(ctx, "GET", cfg.Provider.APIURL+"/v2/sandboxes?limit=4096", nil)
		must(e)
		req.Header.Set("X-API-Key", cfg.Provider.APIKey)
		res, e := hc.Do(req)
		must(e)
		var rows []cube.Sandbox
		e = json.NewDecoder(io.LimitReader(res.Body, 32<<20)).Decode(&rows)
		res.Body.Close()
		must(e)
		if res.StatusCode != 200 || len(rows) >= 4096 {
			panic("complete authoritative inventory unavailable")
		}
		for _, r := range rows {
			if r.Metadata["sandboxd_recovery_id"] == j.ID || r.Metadata["sandboxd_admission_operation"] == j.OperationToken {
				panic("provider candidate already exists; adopt it")
			}
		}
		raw, e := key.Open(j.PlannedCiphertext, j.PlannedNonce)
		must(e)
		var planned struct {
			Supervisor string `json:"supervisor_token"`
		}
		must(json.Unmarshal(raw, &planned))
		if fmt.Sprintf("%x", sha256.Sum256([]byte(planned.Supervisor))) != j.SupervisorSHA256 {
			panic("planned supervisor differs")
		}
		in := cube.CreateRequest{TemplateID: template, TimeoutSeconds: 3600, Lifecycle: &cube.Lifecycle{OnTimeout: "pause", AutoResume: false}, Network: &cube.NetworkPolicy{AllowOut: []string{}, DenyOut: []string{"0.0.0.0/0"}}, EnvVars: map[string]string{"RUNTIMED_HTTP_ADDR": ":3031", "RUNTIMED_HTTP_TOKEN": planned.Supervisor}, Metadata: map[string]string{"sandboxd_id": sandbox, "sandboxd_app_id": appID, "sandboxd_admission_operation": j.OperationToken, "sandboxd_recovery_id": j.ID}, DistributionScope: []string{cfg.Policy.NodeID}}
		body, e := json.Marshal(in)
		must(e)
		if fmt.Sprintf("%x", sha256.Sum256(body)) != j.RequestSHA256 {
			panic("reconstructed request differs from durable intent")
		}
		exclusiveReceipt("retry-proven-rejection-intent.json", map[string]any{"journal_id": j.ID, "request_sha256": j.RequestSHA256, "rejected_master_request": rejectedRequestID, "rejected_log_sha256": rejectedLogSHA, "inventory_count": len(rows), "at": time.Now().UTC()})
		// After this marker exists, any transport/acknowledgment ambiguity requires
		// explicit adoption. Never remove this file to retry again.
		req, e = http.NewRequestWithContext(ctx, "POST", cfg.Provider.APIURL+"/sandboxes", bytes.NewReader(body))
		must(e)
		req.Header.Set("X-API-Key", cfg.Provider.APIKey)
		req.Header.Set("Content-Type", "application/json")
		res, e = hc.Do(req)
		must(e)
		response, e := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		res.Body.Close()
		must(e)
		exclusiveReceipt("retry-http-receipt.PRIVATE.json", map[string]any{"status": res.StatusCode, "body": string(response), "at": time.Now().UTC()})
		if res.StatusCode != http.StatusCreated {
			panic("retry outcome requires explicit review; no further POST allowed")
		}
		must(json.Unmarshal(response, &remote))
		exclusiveReceipt("retry-response.PRIVATE.json", remote)
	}
	if remote.SandboxID == "" || remote.TrafficAccessToken == "" {
		panic("retained authenticated acknowledgment required")
	}
	must(session.AdoptWithIngress(ctx, j.ID, remote.SandboxID, remote.TrafficAccessToken, cfg.ProxyURL))
	mark("exact-replacement-adopted")
}
