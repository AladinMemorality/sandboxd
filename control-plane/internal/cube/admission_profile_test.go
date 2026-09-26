package cube

import (
	"fmt"
	"testing"
)

func TestCompiledAdmissionProfilesKeepProductionEntryPointStrict(t *testing.T) {
	guard := testStorageConfig("/run/cube")
	for _, slots := range []int{4, 6, 8, 12} {
		for _, r := range []AdmissionResources{{1, 1024}, {1, 2048}, {2, 2048}, {2, 1024}, {4, 2048}} {
			t.Run(fmt.Sprintf("%d/%d/%d", slots, r.CPUCount, r.MemoryMB), func(t *testing.T) {
				cfg := AdmissionConfig{MaxActive: slots, CPUCount: r.CPUCount, MemoryMB: r.MemoryMB, WritableDiskMB: 10240, StorageGuard: &guard, Templates: map[string]AdmissionResources{"tpl-reviewed": r}}
				production := slots == 4 && r.CPUCount == 2 && r.MemoryMB == 2048
				benchmark := BenchmarkAdmissionBuild && ((r.CPUCount == 1 && (r.MemoryMB == 1024 || r.MemoryMB == 2048)) || (r.CPUCount == 2 && r.MemoryMB == 2048))
				if err := cfg.validate(); (err == nil) != (production || benchmark) {
					t.Fatalf("compiled admission: %v", err)
				}
				if err := cfg.RequireStorageGuard(); (err == nil) != production {
					t.Fatalf("production entrypoint changed: %v", err)
				}
				if production || benchmark {
					g := admissionGuard{config: cfg}
					actual := Sandbox{TemplateID: "tpl-reviewed", CPUCount: r.CPUCount, MemoryMB: r.MemoryMB, State: "running"}
					if err := g.validRemote(&actual); err != nil {
						t.Fatal(err)
					}
					actual.MemoryMB *= 2
					if g.validRemote(&actual) == nil {
						t.Fatal("actual resource mismatch accepted")
					}
					cfg.Templates["tpl-reviewed"] = AdmissionResources{CPUCount: 8, MemoryMB: 8192}
					if cfg.validate() == nil {
						t.Fatal("template resource mismatch accepted")
					}
				}
			})
		}
	}
}
