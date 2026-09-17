# Cube model relay

The Cube relay is implemented but disabled by default. Its automated tests use a
fake upstream and verify transport, authentication, streaming, cancellation and
revocation. They do **not** establish a successful paid-model run or production
network isolation.

## Configuration gates

`SANDBOXD_CUBE_AGENT_RELAY_ORIGIN` must be the HTTPS origin that routes to this
control plane. An empty value disables the relay. The existing operator-owned
`SANDBOXD_AGENT_PROXY_URL` supplies the fixed private credential proxy upstream.
The only supported agent is `claude-code`; no guest can select a different
provider, target host, URL or credential.

`SANDBOXD_CUBE_AGENT_RELAY_NETWORK_VERIFIED=true` is an operator deployment
attestation, **not a security proof**. Set it only after the patched worker and
live isolation tests establish that allowing the relay does not allow metadata,
host/private-network or cross-sandbox access, including DNS rebinding and IPv6.
The branch's default Cube egress policy remains deny-all; this setting does not
open networking. Do not enable the relay until the reviewed network policy and
public HTTPS routing are deployed and tested together.

A Cube Claude task submitted while the relay is disabled returns HTTP 503 with
`model_relay_disabled` before creating a task or contacting the guest. If the
relay is configured, unsupported agents or a missing project bridge token are
rejected before task launch.

## Capability and metering boundary

The trusted platform task request supplies `Env.BRIDGE_TOKEN`. The control plane
stores only its SHA-256 digest in an expiring task scope and injects a per-task
relay base URL and a capability derived with
`HMAC-SHA256(supervisor_token, "cube-model-relay")`. The supervisor token stays
in the encrypted runtime binding; no global gateway/provider credential is
injected into the VM, its environment, its workspace or its published source.

The guest agent receives only the derived capability and its own project bridge
token. Reserved task-routing variables are server-controlled. The guest adapter
replaces inherited/provider routing and credentials for the Claude subprocess,
then removes the reserved supervisor-routing variables from its environment.

Only these operations self-authenticate outside the normal platform API gate:

- `POST /v1/cube-model/{sandboxID}/{taskID}/v1/messages`
- `POST /v1/cube-model/{sandboxID}/{taskID}/v1/messages/count_tokens`

They require a valid capability, matching project bridge digest, a running Cube
row, a running durable task, an unexpired scope and an authenticated guest status
naming that exact active task. Paths must be canonical; the only query exception
is literal `beta=true`. Duplicate credential/bridge headers are rejected.

Requests go only to the fixed private proxy's
`/claude-code/anthropic/v1/messages` or its `count_tokens` operation. Caller auth,
cookies and project-identity headers are discarded. The verified
`x-baarcha-bridge` is preserved for existing metering; the private credential
proxy injects the real upstream credential outside the VM.

Streaming responses flush immediately. Client cancellation propagates upstream.
Finishing a task deletes its scope atomically with the task-state update, and
open relay streams recheck the scope every 500 ms. VM deletion also removes its
scopes. Upstream redirects fail closed without forwarding credentials or a
redirect location to the guest.

## Remaining rollout validation

Before claiming model support is live: verify the exact worker network patch,
relay-domain policy and public TLS endpoint; exercise a real Claude task through
the existing metering path; validate usage attribution and limits; test task
completion/cancellation revocation on the deployed topology; confirm global
credentials never appear in guest processes, files, logs or source artifacts.
