# Build context: repository root. Candidate only; never use a tenant snapshot.
ARG BASE_IMAGE
FROM ${BASE_IMAGE} AS postgres-build
USER root
RUN apt-get update && apt-get install -y --no-install-recommends bison flex pkg-config libssl-dev zlib1g-dev libreadline-dev libicu-dev \
 && rm -rf /var/lib/apt/lists/*
WORKDIR /tmp/postgres-build
RUN curl -fsSLO https://ftp.postgresql.org/pub/source/v18.6/postgresql-18.6.tar.bz2 \
 && echo '555610c24d53e4316da5b7d3fc25c279d96856d5e0e23ee308c328c5fa881d9f  postgresql-18.6.tar.bz2' | sha256sum -c - \
 && tar -xjf postgresql-18.6.tar.bz2 \
 && cd postgresql-18.6 && ./configure --prefix=/opt/postgresql --with-openssl \
 && make -j4 && make install

FROM ${BASE_IMAGE}
USER root
WORKDIR /tmp
RUN curl -fsSLO https://nodejs.org/dist/v26.12.0/node-v26.12.0-linux-x64.tar.xz \
 && echo 'fd47865ecd6391ebb431fc815e01b9135bf83ded5cf373247e9331e196b7aded  node-v26.12.0-linux-x64.tar.xz' | sha256sum -c - \
 && tar -xJf node-v26.12.0-linux-x64.tar.xz --strip-components=1 -C /usr/local \
 && rm node-v26.12.0-linux-x64.tar.xz \
 && ln -sf /usr/local/bin/node /usr/bin/node \
 && npm install -g --no-audit --no-fund npm@12.2.0 \
 && npm install -g --force --no-audit --no-fund --allow-scripts=@anthropic-ai/claude-code,pnpm pnpm@12.14.0 @anthropic-ai/claude-code@2.1.302 \
 && node --version && pnpm --version && claude --version
COPY --from=postgres-build /opt/postgresql /opt/postgresql
RUN apt-get update && apt-get install -y --no-install-recommends libicu76 libreadline8t64 libssl3t64 zlib1g \
 && rm -rf /var/lib/apt/lists/* \
 && /opt/postgresql/bin/postgres --version \
 && /opt/postgresql/bin/initdb --version \
 && rm -rf /home/sandbox/workspace/app /opt/templates /opt/services/postgres \
 && mkdir -p /home/sandbox/workspace/app /opt/services/postgres
COPY ops/cube/nextjs-postgres/postgres/ /opt/services/postgres/
COPY --chown=1000:1000 ops/cube/nextjs-postgres/app/ /home/sandbox/workspace/app/
COPY --chown=1000:1000 ops/cube/nextjs-postgres/versions.json /home/sandbox/workspace/app/.baarcha/versions.json
COPY --chown=1000:1000 control-plane/internal/designskills/pack/ /home/sandbox/workspace/app/.claude/skills/
ENV RUNTIMED_RUNTIME_PRESET=nextjs NEXT_TELEMETRY_DISABLED=1 DISABLE_AUTOUPDATER=1
WORKDIR /home/sandbox/workspace/app
RUN chown -R 1000:1000 /home/sandbox/workspace/app
USER sandbox
RUN pnpm install --frozen-lockfile \
 && pnpm typecheck && pnpm build
USER root
RUN rm -rf /root/.npm /home/sandbox/.local/share/pnpm/store /home/sandbox/.cache/pnpm-store \
 && test ! -e /home/sandbox/.baarcha-postgres \
 && test ! -e /home/sandbox/.claude.json \
 && test ! -e /home/sandbox/.claude/.credentials.json
USER sandbox
WORKDIR /home/sandbox/workspace
# Keep reviewed Cube bootstrap, runtime supervisor and proxy behavior.
