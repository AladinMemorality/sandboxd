package docker

import (
	"encoding/json"
	"testing"
)

func TestInspectPreservesLatestStart(t *testing.T) {
	var container ContainerJSON
	// Creation predates the latest start; uptime must reset after a wake.
	input := `{"Id":"sandbox","Created":"2026-08-01T00:00:00Z","State":{"Running":true,"StartedAt":"2026-09-05T12:34:56.123456789Z"}}`
	if err := json.Unmarshal([]byte(input), &container); err != nil {
		t.Fatal(err)
	}
	if container.State.StartedAt != "2026-09-05T12:34:56.123456789Z" {
		t.Fatalf("lost container start: %q", container.State.StartedAt)
	}
	encoded, err := json.Marshal(container)
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		State struct{ StartedAt string }
	}
	if err := json.Unmarshal(encoded, &response); err != nil {
		t.Fatal(err)
	}
	if response.State.StartedAt != container.State.StartedAt {
		t.Fatal("start time missing from the inspection API response")
	}
}
