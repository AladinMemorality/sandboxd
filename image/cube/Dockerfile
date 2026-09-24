# Build context: repository root. BASE_IMAGE must contain reviewed, credential-
# free app files/dependencies. Never build this from a live tenant's filesystem.
ARG BASE_IMAGE=sandboxd-base:0.3.0
FROM golang:1.22-bookworm AS builder
WORKDIR /src
COPY control-plane/go.mod control-plane/go.sum ./
RUN go mod download
COPY control-plane/ ./
RUN CGO_ENABLED=0 go build -trimpath -o /runtimed ./cmd/runtimed \
 && CGO_ENABLED=0 go build -trimpath -o /cube-init ./cmd/cube-init \
 && CGO_ENABLED=0 go build -trimpath -o /cube-template ./cmd/cube-template
FROM ${BASE_IMAGE}
USER root
COPY --from=builder /runtimed /usr/local/bin/runtimed
COPY --from=builder /cube-init /usr/local/bin/cube-init
COPY --from=builder /cube-template /usr/local/bin/cube-template
RUN mkdir -p /home/sandbox/workspace/app \
 && chown sandbox:sandbox /home/sandbox/workspace /home/sandbox/workspace/app
USER sandbox
WORKDIR /home/sandbox/workspace
ARG RUNTIME_PRESET=react-vite
ARG CUBE_REVERSE_EGRESS=0
ENV RUNTIMED_RUNTIME_PRESET=${RUNTIME_PRESET} \
    RUNTIMED_CUBE_REVERSE_EGRESS=${CUBE_REVERSE_EGRESS}
# Seed only reviewed starter files during the build. This avoids copying a
# dependency tree on every create. The preparer refuses a nonempty destination.
# FastAPI's starter also needs its environment prepared before snapshotting.
# Existing owner environments are transferred separately and never rebuilt here.
RUN if [ "${CUBE_REVERSE_EGRESS}" = 1 ]; then \
      node -e 'const [major,minor]=process.versions.node.split(".").map(Number); if(major!==22 || minor<21) throw new Error("Cube proxy profile requires reviewed Node 22.21+")'; \
    fi \
 && /usr/local/bin/cube-template "${RUNTIME_PRESET}" \
 && if [ "${RUNTIME_PRESET}" = fastapi ]; then \
      cd /home/sandbox/workspace/app \
      && python3 -m venv .venv \
      && .venv/bin/pip install --no-cache-dir -r requirements.txt; \
    fi
# Cube's template readiness port must be 49983 (tokenless bootstrap), not3031.
# No supervisor/app starts until Cubelet delivers fresh per-instance credentials.
ENTRYPOINT ["/usr/bin/tini", "--"]
CMD ["/usr/local/bin/cube-init"]
