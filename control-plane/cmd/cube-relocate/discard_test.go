package main

import (
	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
	"testing"
)

func TestDiscardRequiresUncommittedOwnedTarget(t *testing.T) {
	j := store.CubeRelocation{ID: "move", SandboxID: "project", AppID: "app", SourceRuntimeID: "source", TargetWorker: "vps", TargetKey: "relocation:move", TemplateID: "template"}
	targetRuntime := &cube.Sandbox{SandboxID: "target", TemplateID: "template", Metadata: map[string]string{"sandboxd_id": "project", "sandboxd_app_id": "app", "sandboxd_relocation_id": "move", "sandboxd_admission_operation": "creation"}}
	tg := target{Relocation: j, Runtime: targetRuntime, Admission: cube.AdmissionRecord{Key: j.TargetKey, Token: "creation"}}
	binding := &store.RuntimeBinding{RuntimeID: "source"}
	if !validDiscardTarget(j, tg, targetRuntime, binding) {
		t.Fatal("owned uncommitted target rejected")
	}
	for _, runtime := range []string{"target", "other"} {
		if validDiscardTarget(j, tg, targetRuntime, &store.RuntimeBinding{RuntimeID: runtime}) {
			t.Fatal("changed/committed binding accepted")
		}
	}
	for _, key := range []string{"sandboxd_id", "sandboxd_app_id", "sandboxd_relocation_id", "sandboxd_admission_operation"} {
		old := targetRuntime.Metadata[key]
		targetRuntime.Metadata[key] = "different"
		if validDiscardTarget(j, tg, targetRuntime, binding) {
			t.Fatal("wrong ownership accepted", key)
		}
		targetRuntime.Metadata[key] = old
	}
	j.TargetWorker = "b200-01"
	if validDiscardTarget(j, tg, targetRuntime, binding) {
		t.Fatal("B200 target accepted")
	}
	j.TargetWorker = "vps"
	j.SourceRuntimeID = "target"
	binding.RuntimeID = "target"
	if validDiscardTarget(j, tg, targetRuntime, binding) {
		t.Fatal("source runtime accepted for discard")
	}
}
