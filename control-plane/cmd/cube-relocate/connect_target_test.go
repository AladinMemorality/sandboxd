package main

import (
	"testing"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

func TestConnectedTargetLeaseCannotCommitSourceOrUncertainReservation(t *testing.T) {
	j := store.CubeRelocation{TargetKey: "relocation:one", SourceRuntimeID: "source", TargetWorker: "vps", TemplateID: "tpl-one"}
	base := cube.AdmissionRecord{Key: j.TargetKey, RuntimeID: "target", TemplateID: "tpl-one", WorkerID: "vps", Operation: "connect", State: "active", Charged: 1, Token: "fresh-lease"}
	if !validConnectedTargetLease(j, "target", base) {
		t.Fatal("verified connected target rejected")
	}
	for _, change := range []func(*cube.AdmissionRecord){
		func(a *cube.AdmissionRecord) { a.Key = "app:source" },
		func(a *cube.AdmissionRecord) { a.RuntimeID = "source" },
		func(a *cube.AdmissionRecord) { a.TemplateID = "other" },
		func(a *cube.AdmissionRecord) { a.WorkerID = "other" },
		func(a *cube.AdmissionRecord) { a.Operation = "create" },
		func(a *cube.AdmissionRecord) { a.State = "pending" },
		func(a *cube.AdmissionRecord) { a.Charged = 0 },
		func(a *cube.AdmissionRecord) { a.Token = "" },
	} {
		a := base
		change(&a)
		if validConnectedTargetLease(j, "target", a) {
			t.Fatal("unsafe admission accepted")
		}
	}
	a := base
	a.RuntimeID = "source"
	if validConnectedTargetLease(j, "source", a) {
		t.Fatal("source accepted as replacement")
	}
}
