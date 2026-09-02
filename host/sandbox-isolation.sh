#!/bin/bash
# Network isolation for sandbox containers, applied on the HOST.
#
# What it enforces, on the Docker bridge that carries the sandboxes:
#   1. sandbox -> sandbox: dropped. Tenants cannot reach each other's ports.
#      Traffic FROM the infrastructure containers (Traefik routing previews,
#      the control plane probing them) is still allowed.
#   2. sandbox -> control plane / Traefik: dropped, except the agent auth proxy
#      port on the control plane. A sandbox has no business talking to the
#      API that creates and deletes sandboxes.
#   3. sandbox -> private address space (RFC1918, CGNAT, link-local, including
#      the ZeroTier range): dropped. The coding agent reaches the model
#      gateway through the auth proxy (rule 2), never directly.
#   4. sandbox -> port 25: dropped (no outbound SMTP from a hosted sandbox).
#   5. new connections per source above SANDBOX_NEW_CONN_RATE/s: dropped, so
#      a sandbox cannot be used to flood anything from this host's address.
#
# Everything else (public internet on any other port) stays open: builds
# need registries, apps need their APIs. Counters on every drop rule make
# `nft list table inet sandbox_platform` a quick abuse dashboard.
#
# Idempotent: it rewrites only its own table. Run after `docker compose up`
# (the deploy script does) and at boot (the systemd unit next to this file);
# the infrastructure IPs are resolved at run time because compose may hand
# them out differently after a recreate.
set -euo pipefail

SRC_DIR="${SANDBOXD_SRC_DIR:-/opt/sandboxd/src}"
ENV_FILE="$SRC_DIR/.env"
NETWORK="${SANDBOXD_NETWORK:-$(grep -E '^SANDBOXD_NETWORK=' "$ENV_FILE" 2>/dev/null | cut -d= -f2)}"
NETWORK="${NETWORK:-sandboxd_net}"
RATE="${SANDBOX_NEW_CONN_RATE:-100}"
PROXY_PORT="${SANDBOXD_AGENT_PROXY_PORT:-9100}"

# Same-bridge traffic only reaches the forward hook with br_netfilter loaded;
# without it the bridge switches frames at layer 2 and no firewall sees them.
modprobe br_netfilter
sysctl -q -w net.bridge.bridge-nf-call-iptables=1

net_id=$(docker network inspect "$NETWORK" -f '{{.Id}}')
bridge="br-${net_id:0:12}"
ip link show "$bridge" > /dev/null

infra_ips=()
for name in traefik sandboxd; do
  cid=$(docker ps -q --filter "label=com.docker.compose.service=$name" | head -1)
  [ -n "$cid" ] || { echo "sandbox-isolation: compose service '$name' is not running" >&2; exit 1; }
  ip=$(docker inspect -f "{{with index .NetworkSettings.Networks \"$NETWORK\"}}{{.IPAddress}}{{end}}" "$cid")
  [ -n "$ip" ] || { echo "sandbox-isolation: no address for $name on $NETWORK" >&2; exit 1; }
  infra_ips+=("$ip")
done
infra_set=$(IFS=,; echo "${infra_ips[*]}")

# The model gateway (SANDBOXD_ANTHROPIC_UPSTREAM) is reached by the coding
# agent THROUGH the control plane's auth proxy, which injects the key; the
# sandbox itself never needs a route to it. SANDBOX_ALLOW_MODEL_GATEWAY=1
# punches that route anyway, for an install whose agents still call the
# upstream directly. Off by default: with it on, any code in a sandbox can
# use the gateway, and if the gateway has no key, for free.
upstream=$(grep -E '^SANDBOXD_ANTHROPIC_UPSTREAM=' "$ENV_FILE" 2>/dev/null | cut -d= -f2- | sed -E 's#^[a-z]+://##; s#/.*$##')
gateway_rule=""
if [ "${SANDBOX_ALLOW_MODEL_GATEWAY:-0}" = "1" ] && [[ "$upstream" =~ ^([0-9.]+):([0-9]+)$ ]]; then
  gateway_rule="iifname \"$bridge\" ip daddr ${BASH_REMATCH[1]} tcp dport ${BASH_REMATCH[2]} accept comment \"model gateway\""
fi

nft -f - <<EOF
table inet sandbox_platform
delete table inet sandbox_platform
table inet sandbox_platform {
  set infra { type ipv4_addr; elements = { $infra_set } }
  set private { type ipv4_addr; flags interval; elements = {
    10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 100.64.0.0/10, 169.254.0.0/16 } }

  chain forward {
    type filter hook forward priority -10; policy accept;

    # Replies on connections that were allowed to open (a preview request
    # from Traefik, a package download) are never the sandbox "reaching"
    # anything; without this line every rule below also eats the answers.
    ct state established,related accept

    # The infrastructure containers share this bridge. Traefik routing a
    # preview, the control plane probing a sandbox, the auth proxy calling the
    # model gateway, a git import cloning from GitHub: none of that is a
    # sandbox reaching out, so nothing below applies to it.
    iifname "$bridge" ip saddr @infra accept

    # A sandbox reaching the control plane: only the agent auth proxy.
    iifname "$bridge" oifname "$bridge" ip daddr @infra tcp dport $PROXY_PORT accept
    iifname "$bridge" oifname "$bridge" ip daddr @infra counter drop comment "sandbox to control plane"

    # A sandbox reaching another sandbox.
    iifname "$bridge" oifname "$bridge" counter drop comment "sandbox to sandbox"

    # Out of the bridge: no private networks, no SMTP, a ceiling on new
    # connections per sandbox, then the public internet.
    $gateway_rule
    iifname "$bridge" ip daddr @private counter drop comment "sandbox to private range"
    iifname "$bridge" tcp dport 25 counter drop comment "sandbox smtp"
    iifname "$bridge" ct state new meter sandbox_conn_rate size 65535 { ip saddr limit rate over $RATE/second burst $RATE packets } counter drop comment "sandbox connection rate"
  }
}
EOF

echo "sandbox-isolation: applied on $bridge (infra: $infra_set, rate: $RATE new conn/s per sandbox${gateway_rule:+, model gateway allowed})"
