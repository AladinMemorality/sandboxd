#!/usr/bin/env bash
# Public Docker Hub cache on disposable Linux CI runners only.
# https://docs.cloud.google.com/artifact-registry/docs/pull-cached-dockerhub-images
set -euo pipefail
[[ ${GITHUB_ACTIONS:-} == true && ${RUNNER_OS:-} == Linux ]] || {
  echo 'This helper is restricted to Linux GitHub Actions runners' >&2
  exit 2
}
sudo python3 - <<'PYCONFIG'
import json, pathlib
path = pathlib.Path('/etc/docker/daemon.json')
config = json.loads(path.read_text()) if path.exists() else {}
mirrors = config.get('registry-mirrors', [])
config['registry-mirrors'] = list(dict.fromkeys(['https://mirror.gcr.io', *mirrors]))
path.parent.mkdir(parents=True, exist_ok=True)
path.write_text(json.dumps(config) + '\n')
PYCONFIG
sudo dockerd --validate --config-file=/etc/docker/daemon.json
sudo systemctl restart docker
docker info --format '{{json .RegistryConfig.Mirrors}}'
