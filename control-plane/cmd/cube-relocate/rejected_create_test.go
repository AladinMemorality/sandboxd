package main

import (
	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
	"testing"
)

func TestRetryOnlyUnambiguouslyRejectedMatchingCreate(t *testing.T) {
	j := store.CubeRelocation{ID: "move", SandboxID: "app-sandbox", AppID: "app", SourceRuntimeID: "source", TargetWorker: "vps", TargetKey: "relocation:move", TemplateID: "template"}
	a := cube.AdmissionRecord{Key: j.TargetKey, WorkerID: "vps", TemplateID: "template", Token: "operation", State: "pending", Operation: "create", Charged: 1}
	fixture := target{Relocation: j, Admission: a, SupervisorToken: "private"}
	p := rejectedCreate{RelocationID: j.ID, OperationToken: a.Token, RequestID: "request", HTTPStatus: 500, FailureCode: 130597}
	if !validRejectedCreate(j, fixture, a, p) {
		t.Fatal("matching pre-allocation rejection refused")
	}
	cases := []struct {
		name   string
		mutate func(*store.CubeRelocation, *target, *cube.AdmissionRecord, *rejectedCreate)
	}{
		{"timeout", func(_ *store.CubeRelocation, _ *target, _ *cube.AdmissionRecord, p *rejectedCreate) {
			p.FailureCode = 0
		}},
		{"wrong operation", func(_ *store.CubeRelocation, _ *target, _ *cube.AdmissionRecord, p *rejectedCreate) {
			p.OperationToken = "other"
		}},
		{"unacknowledged HTTP", func(_ *store.CubeRelocation, _ *target, _ *cube.AdmissionRecord, p *rejectedCreate) { p.HTTPStatus = 0 }},
		{"changed reservation", func(_ *store.CubeRelocation, _ *target, a *cube.AdmissionRecord, _ *rejectedCreate) {
			a.Token = "other"
		}},
		{"created target", func(_ *store.CubeRelocation, tg *target, _ *cube.AdmissionRecord, _ *rejectedCreate) {
			tg.Runtime = &cube.Sandbox{SandboxID: "target"}
		}},
		{"changed source", func(j *store.CubeRelocation, _ *target, _ *cube.AdmissionRecord, _ *rejectedCreate) {
			j.SourceRuntimeID = "other"
		}},
		{"changed config", func(j *store.CubeRelocation, _ *target, _ *cube.AdmissionRecord, _ *rejectedCreate) {
			j.ConfigRevision++
		}},
		{"B200", func(j *store.CubeRelocation, tg *target, a *cube.AdmissionRecord, _ *rejectedCreate) {
			j.TargetWorker = "b200-01"
			tg.Relocation = *j
			a.WorkerID = "b200-01"
			tg.Admission = *a
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			jj, tt, aa, pp := j, fixture, a, p
			c.mutate(&jj, &tt, &aa, &pp)
			if validRejectedCreate(jj, tt, aa, pp) {
				t.Fatal("unsafe retry accepted")
			}
		})
	}
}
