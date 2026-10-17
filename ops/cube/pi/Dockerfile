# Build context: repository root. Layer onto the reviewed Next.js/Postgres
# candidate, never a tenant snapshot. Existing Cube defaults are not changed.
ARG BASE_IMAGE
FROM golang:1.25.7-bookworm AS runtime-build
WORKDIR /src
COPY control-plane/go.mod control-plane/go.sum ./
RUN go mod download
COPY control-plane/ ./
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o /out/runtimed ./cmd/runtimed

FROM ${BASE_IMAGE}
USER root
COPY ops/cube/pi/agent/package.json ops/cube/pi/agent/package-lock.json /opt/baarcha/pi/
RUN cd /opt/baarcha/pi && npm ci --ignore-scripts --no-audit --no-fund \
 && ln -s /opt/baarcha/pi/node_modules/.bin/pi /usr/local/bin/pi \
 && pi --version \
 && rm -rf /root/.npm
COPY --from=runtime-build /out/runtimed /usr/local/bin/runtimed
ENV PI_OFFLINE=1 PI_TELEMETRY=0
USER sandbox
WORKDIR /home/sandbox/workspace
