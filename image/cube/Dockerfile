# Build context: repository root. BASE_IMAGE must contain reviewed, credential-
# free app files/dependencies. Never build this from a live tenant's filesystem.
ARG BASE_IMAGE=sandboxd-base:0.3.0
FROM golang:1.22-bookworm AS builder
WORKDIR /src
COPY control-plane/go.mod control-plane/go.sum ./
RUN go mod download
COPY control-plane/ ./
RUN CGO_ENABLED=0 go build -trimpath -o /runtimed ./cmd/runtimed \
 && CGO_ENABLED=0 go build -trimpath -o /cube-init ./cmd/cube-init
FROM ${BASE_IMAGE}
USER root
COPY --from=builder /runtimed /usr/local/bin/runtimed
COPY --from=builder /cube-init /usr/local/bin/cube-init
USER sandbox
WORKDIR /home/sandbox/workspace
# Cube's template readiness port must be 49983 (tokenless bootstrap), not3031.
# No supervisor/app starts until Cubelet delivers fresh per-instance credentials.
ENTRYPOINT ["/usr/bin/tini", "--"]
CMD ["/usr/local/bin/cube-init"]
