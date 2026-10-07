package main

import (
	"bytes"
	"encoding/json"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
	"testing"
)

func TestRejectionMustBindUniqueTerminalRequest(t *testing.T) {
	j := &store.CubeRecoveryJournal{ID: journalID, SandboxID: sandbox, AppID: appID, OperationToken: "owned-operation"}
	j.Target.TemplateID = template
	request, _ := json.Marshal(map[string]any{"requestID": rejectedRequestID, "labels": map[string]string{"sandboxd_recovery_id": j.ID, "sandboxd_id": j.SandboxID, "sandboxd_app_id": j.AppID, "sandboxd_admission_operation": j.OperationToken, "cube.master.appsnapshot.template.id": template}})
	lines := []string{"CreateSandbox:" + string(request), "Filter/disk select:10.0.2.15, StorageDiskUsagePer:66.68391338262485, DiskUsageMaxPercent:65", `CreateSandbox_rsp fail:{"RequestID":"` + rejectedRequestID + `","ret":{"ret_code":130597,"ret_msg":"no more resource"}}`}
	encode := func(lines []string) []byte {
		var b bytes.Buffer
		for _, s := range lines {
			raw, _ := json.Marshal(map[string]string{"RequestId": rejectedRequestID, "LogContent": s})
			b.Write(raw)
			b.WriteByte('\n')
		}
		return b.Bytes()
	}
	good := encode(lines)
	if e := validateRejectionRecords(good, j); e != nil {
		t.Fatal(e)
	}
	for name, raw := range map[string][]byte{
		"other-operation":      bytes.ReplaceAll(good, []byte("owned-operation"), []byte("different-operation")),
		"missing-terminal":     encode(lines[:2]),
		"duplicate-terminal":   encode(append(append([]string{}, lines...), lines[2])),
		"generic-failure":      bytes.ReplaceAll(good, []byte("130597"), []byte("500")),
		"other-template":       bytes.ReplaceAll(good, []byte(template), []byte("different-template")),
		"missing-disk-refusal": encode([]string{lines[0], lines[2]}),
	} {
		t.Run(name, func(t *testing.T) {
			if validateRejectionRecords(raw, j) == nil {
				t.Fatal("accepted unproven rejection")
			}
		})
	}
	if validateRejection(good, j) == nil {
		t.Fatal("synthetic proof must fail immutable production log hash")
	}
}
