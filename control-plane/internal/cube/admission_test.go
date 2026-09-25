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
