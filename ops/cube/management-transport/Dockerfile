# Supply an explicitly reviewed immutable Debian bookworm-slim image digest.
ARG DEBIAN_IMAGE
FROM ${DEBIAN_IMAGE}
ARG DEBIAN_IMAGE
ARG SOCAT_VERSION=1.7.4.4-2
RUN printf '%s\n' "$DEBIAN_IMAGE" | grep -Eq '^debian:bookworm-slim@sha256:[a-f0-9]{64}$' \
    && apt-get update && apt-get install -y --no-install-recommends socat=${SOCAT_VERSION} \
    && rm -rf /var/lib/apt/lists/*
COPY relay.sh healthcheck.sh /usr/local/bin/
RUN chmod 0555 /usr/local/bin/relay.sh /usr/local/bin/healthcheck.sh
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/relay.sh"]
