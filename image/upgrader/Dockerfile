# sandboxd-upgrader — runs ./upgrade.sh on behalf of the control plane so an
# in-place upgrade can outlive the control-plane container it replaces.
# docker:cli already ships the compose plugin and git; upgrade.sh needs bash.
FROM docker:27-cli
RUN apk add --no-cache bash curl coreutils
ENTRYPOINT ["/bin/bash"]
