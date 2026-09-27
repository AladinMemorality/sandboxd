package cube

import "testing"

func TestAdmissionConfigBoundedUniformExplicit(t *testing.T) {
	good := `{"max_active":12,"cpu_count":2,"memory_mb":2048,"templates":{"tpl-reviewed":{"cpu_count":2,"memory_mb":2048}}}`
	if _, err := ParseAdmissionConfig(good); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"", good + "x", good + "{}", `{"max_active":13,"cpu_count":2,"memory_mb":2048,"templates":{"tpl-reviewed":{"cpu_count":2,"memory_mb":2048}}}`, `{"max_active":12,"cpu_count":2,"memory_mb":2048,"templates":{"tpl-reviewed":{"cpu_count":2,"memory_mb":4096}}}`, `{"max_active":12,"cpu_count":2,"memory_mb":2048,"templates":{},"unchecked":true}`} {
		if _, err := ParseAdmissionConfig(raw); err == nil {
			t.Fatalf("unsafe config accepted %s", raw)
		}
	}
}

func TestPinnedHundredRequiresFullHeadroomAndStorageCeiling(t *testing.T) {
	cfg := AdmissionConfig{MaxActive: 100, CPUCount: 2, MemoryMB: 2048,
		NodeID: "node-reviewed", HostCPUMillis: 230000, HostMemoryMB: 216000,
		Templates: map[string]AdmissionResources{"tpl-reviewed": {CPUCount: 2, MemoryMB: 2048}}}
	if err := cfg.validate(); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*AdmissionConfig){
		func(c *AdmissionConfig) { c.HostCPUMillis-- },
		func(c *AdmissionConfig) { c.HostMemoryMB-- },
		func(c *AdmissionConfig) { c.MaxActive++ },
		func(c *AdmissionConfig) { c.NodeID = "" },
	} {
		changed := cfg
		change(&changed)
		if err := changed.validate(); err == nil {
			t.Fatal("unfunded or unpinned worker accepted")
		}
	}
}
