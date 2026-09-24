// Package cube implements the CubeSandbox v0.7.1 control-plane API.
// It deliberately does not construct or contact sandbox preview/control URLs.
package cube

import "net/http"

// Config must come from operator configuration, never a sandbox request.
// Plain HTTP is supported for a private control network; use HTTPS otherwise.
type Config struct {
	APIURL     string
	APIKey     string
	HTTPClient *http.Client
}

type Lifecycle struct {
	OnTimeout  string `json:"onTimeout"`
	AutoResume bool   `json:"autoResume"`
}

// NetworkPolicy is mandatory on creation. These fields are policy, not a
// security proof: the deployed Cube egress enforcement must also be verified.
// The caller must supply policy from trusted configuration, not guest input.
type NetworkPolicy struct {
	AllowPublicTraffic bool     `json:"allowPublicTraffic"`
	AllowOut           []string `json:"allowOut"`
	DenyOut            []string `json:"denyOut"`
	MaskRequestHost    string   `json:"maskRequestHost,omitempty"`
}

type CreateRequest struct {
	TemplateID          string            `json:"templateID"`
	EnvVars             map[string]string `json:"envVars,omitempty"`
	Metadata            map[string]string `json:"metadata,omitempty"`
	TimeoutSeconds      int               `json:"timeout,omitempty"`
	Lifecycle           *Lifecycle        `json:"lifecycle"`
	AllowInternetAccess bool              `json:"allow_internet_access"`
	Network             *NetworkPolicy    `json:"network"`
	Backend             string            `json:"backend,omitempty"`
}

type ConnectRequest struct {
	TimeoutSeconds int `json:"timeout,omitempty"`
}

// Sandbox unifies create/connect and detail responses. State and resource
// counts are only supplied by Get. Domain and access tokens are untrusted
// response data; do not use Domain as an arbitrary forwarding target.
type Sandbox struct {
	SandboxID          string            `json:"sandboxID"`
	TemplateID         string            `json:"templateID"`
	ClientID           string            `json:"clientID"`
	Domain             string            `json:"domain"`
	State              string            `json:"state"`
	EnvdVersion        string            `json:"envdVersion"`
	EnvdAccessToken    string            `json:"envdAccessToken"`
	TrafficAccessToken string            `json:"trafficAccessToken"`
	CPUCount           int               `json:"cpuCount"`
	MemoryMB           int               `json:"memoryMB"`
	Metadata           map[string]string `json:"metadata"`
}

type SnapshotRequest struct {
	Name    string `json:"name,omitempty"`
	Backend string `json:"backend,omitempty"`
}

type Snapshot struct {
	SnapshotID   string   `json:"snapshotID"`
	Names        []string `json:"names"`
	Backend      string   `json:"backend,omitempty"`
	RemoteStatus string   `json:"remoteStatus,omitempty"`
}
