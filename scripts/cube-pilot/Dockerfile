# Disposable fixture only. The fake OpenCode CLI tests task transport, not AI.
FROM baarcha-resume-bench:v1
USER root
COPY image/cube/runtimed /usr/local/bin/runtimed
COPY image/cube/cube-init /usr/local/bin/cube-init
COPY scripts/cube-pilot/fake-opencode /usr/local/bin/opencode
COPY scripts/cube-pilot/probe.mjs /home/sandbox/workspace/app/pilot-probe.mjs
COPY scripts/cube-pilot/sandbox.yaml /home/sandbox/workspace/app/sandbox.yaml
RUN chmod 755 /usr/local/bin/opencode \
 && chown -R sandbox:sandbox /home/sandbox/workspace/app
USER sandbox
ENTRYPOINT ["/usr/bin/tini", "--"]
CMD ["/usr/local/bin/cube-init"]
