#!/usr/bin/env python3
"""Fresh worker VM only: pull upstream-selected dependencies and pin resolved digests.

Does not start containers. Writes only the new deployment's input environment
and an image manifest; never prints credentials or full docker inspect records.
"""
import concurrent.futures
import json
from pathlib import Path
import re
import socket
import subprocess

assert socket.gethostname() == "baarcha-cube-worker-01"
root = Path("/root/cube-production")
env_path = root / "input/cube-install.env"
assert env_path.is_file()
images = {
    "CUBE_SANDBOX_MYSQL_IMAGE": "cube-sandbox-image.tencentcloudcr.com/opensource/mysql:8.0",
    "CUBE_SANDBOX_REDIS_IMAGE": "cube-sandbox-image.tencentcloudcr.com/opensource/redis:7-alpine",
    "CUBE_SANDBOX_MINIO_IMAGE": "cube-sandbox-int.tencentcloudcr.com/cube-sandbox/minio:RELEASE.2025-09-07T16-13-09Z",
    "CUBE_PROXY_COREDNS_IMAGE": "cube-sandbox-image.tencentcloudcr.com/opensource/coredns/coredns:1.14.2",
    "CUBE_SANDBOX_CUBE_PROXY_IMAGE": "cube-sandbox-int.tencentcloudcr.com/cube-sandbox/cube-proxy:v0.7.1",
    "CUBE_SANDBOX_CUBE_LCM_IMAGE": "cube-sandbox-int.tencentcloudcr.com/cube-sandbox/cube-lifecycle-manager:v0.7.1",
    "CUBE_SANDBOX_CUBE_EGRESS_IMAGE": "cube-sandbox-int.tencentcloudcr.com/cube-sandbox/cube-egress:v0.7.1",
}


def pin(item):
    key, image = item
    with (root / ("pull-" + key + ".log")).open("w") as output:
        subprocess.run(["docker", "pull", image], stdout=output, stderr=subprocess.STDOUT,
                       timeout=600, check=True)
    info = json.loads(subprocess.check_output(["docker", "image", "inspect", image], text=True))[0]
    repository = image.rsplit(":", 1)[0]
    choices = [digest for digest in info["RepoDigests"] if digest.startswith(repository + "@sha256:")]
    assert len(choices) == 1 and re.fullmatch(r".+@sha256:[0-9a-f]{64}", choices[0])
    print("Pinned " + key + " " + choices[0], flush=True)
    return {"variable": key, "requested": image, "resolved": choices[0], "image_id": info["Id"]}


with concurrent.futures.ThreadPoolExecutor(max_workers=3) as pool:
    manifest = list(pool.map(pin, images.items()))
lines = [line for line in env_path.read_text().splitlines() if line.split("=", 1)[0] not in images]
lines += [row["variable"] + "=" + row["resolved"] for row in manifest]
env_path.write_text("\n".join(lines) + "\n")
env_path.chmod(0o600)
(root / "dependency-images.json").write_text(json.dumps(manifest, indent=2) + "\n")
print("All dependency images pinned; no containers started by this script.")
