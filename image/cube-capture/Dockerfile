# Build context: sandboxd repository root. The base must be a reviewed image
# built by platform/landing/services/capture/Dockerfile, never a tenant image.
ARG CAPTURE_WORKER_IMAGE=baarcha-capture-worker:release
FROM golang:1.22-bookworm AS builder
WORKDIR /src
COPY control-plane/go.mod control-plane/go.sum ./
RUN go mod download
COPY control-plane/ ./
RUN CGO_ENABLED=0 go build -trimpath -o /capture-init ./cmd/capture-init
FROM ${CAPTURE_WORKER_IMAGE}
USER root
COPY --from=builder /capture-init /usr/local/bin/capture-init
USER 1000:1000
ENTRYPOINT ["/usr/local/bin/capture-init"]
