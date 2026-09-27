# QEMU tooling for an isolated worker VM on a shared CPU/GPU host.
# Cube services belong inside that VM; this image is not a Cube worker itself.
FROM ubuntu@sha256:2260313b31c8c011cd2eebe728008efac1b3982be73eb71348ea2648d2c0e09b
RUN apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
       qemu-system-x86 qemu-utils cloud-image-utils ca-certificates \
    && dpkg-query -W > /installed-packages.txt \
    && rm -rf /var/lib/apt/lists/*
ENTRYPOINT ["/usr/bin/qemu-system-x86_64"]
