package api

import (
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

// The application cannot redact a request query already recorded by Traefik.
// Enforce this deployment boundary alongside the capability handoff tests.
func TestPreviewCapabilityQueryExcludedFromTraefikAccessLog(t *testing.T) {
	data, err := os.ReadFile("../../../traefik/traefik.yml")
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		AccessLog struct {
			Fields struct {
				Names   map[string]string `yaml:"names"`
				Headers struct {
					DefaultMode string `yaml:"defaultMode"`
				} `yaml:"headers"`
			} `yaml:"fields"`
		} `yaml:"accessLog"`
	}
	if err = yaml.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	if config.AccessLog.Fields.Names["RequestPath"] != "drop" {
		t.Fatal("Traefik must exclude capability-bearing request paths/queries")
	}
	if config.AccessLog.Fields.Names["RequestHost"] != "keep" {
		t.Fatal("activity tracking still requires RequestHost")
	}
	if config.AccessLog.Fields.Headers.DefaultMode != "drop" {
		t.Fatal("platform cookies must not be logged")
	}
}
