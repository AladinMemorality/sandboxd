package runtime

import "testing"

func TestMotionTargetOverlayRequiresCapabilityAndPreservesOriginal(t *testing.T) {
	original := map[string]string{"STUDIO_WORKER_URL": "http://172.19.0.1:8332", "STUDIO_WORKER_KEY": "owned-key", "APP_ORIGIN": "https://stable.preview", "UNCHANGED": "yes"}
	for _, status := range []*Status{nil, {}, {Capabilities: []string{"unrelated"}}} {
		if _, err := MotionStudioEnvironment(original, status); err == nil {
			t.Fatal("old template accepted")
		}
	}
	changed, err := MotionStudioEnvironment(original, &Status{Capabilities: []string{MotionWorkerCapability}})
	if err != nil {
		t.Fatal(err)
	}
	if changed["STUDIO_WORKER_URL"] != MotionWorkerURL || original["STUDIO_WORKER_URL"] != "http://172.19.0.1:8332" || changed["STUDIO_WORKER_KEY"] != original["STUDIO_WORKER_KEY"] || changed["APP_ORIGIN"] != original["APP_ORIGIN"] {
		t.Fatal("rollback config or stable identity changed")
	}
	changed["UNCHANGED"] = "other"
	if original["UNCHANGED"] != "yes" {
		t.Fatal("overlay aliases original map")
	}
}

func TestMotionScopeCannotBeGrantedByTenantEnvironment(t *testing.T) {
	for _, env := range []map[string]string{{"STUDIO_WORKER_URL": "http://legacy"}, {"STUDIO_WORKER_KEY": "owned-key"}, {"STUDIO_WORKER_URL": MotionWorkerURL}} {
		if ValidateMotionStudioScope(env, false) == nil {
			t.Fatal("missing exact app capability accepted")
		}
		if ValidateMotionStudioScope(env, true) != nil {
			t.Fatal("operator-selected scope refused")
		}
	}
	if ValidateMotionStudioScope(map[string]string{"UNRELATED": "value"}, false) != nil {
		t.Fatal("ordinary config refused")
	}
}
