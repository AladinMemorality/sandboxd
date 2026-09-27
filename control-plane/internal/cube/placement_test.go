package cube

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestProjectPlacementScopeIsExplicitAndCopied(t *testing.T) {
	in := CreateRequest{TemplateID: "template-one", Network: &NetworkPolicy{}, DistributionScope: []string{"node-b200"}}
	out, err := prepareCreate(in)
	if err != nil {
		t.Fatal(err)
	}
	in.DistributionScope[0] = "changed"
	if out.DistributionScope[0] != "node-b200" {
		t.Fatal("placement aliased caller memory")
	}
	body, _ := json.Marshal(out)
	if !strings.Contains(string(body), `"distributionScope":["node-b200"]`) {
		t.Fatal("wire placement missing")
	}
	for _, node := range []string{"10.254.240.2", "fd00::2"} {
		in.DistributionScope = []string{node}
		if _, err = prepareCreate(in); err != nil {
			t.Fatalf("Cube accepts an explicit node IP: %s: %v", node, err)
		}
	}
	for _, scope := range [][]string{{"one", "two"}, {"../bad"}, {""}} {
		in.DistributionScope = scope
		if _, err = prepareCreate(in); err == nil {
			t.Fatal("invalid placement accepted")
		}
	}
}
