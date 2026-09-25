"""Fresh-worker API authentication through actual UDS relays; no guest writes."""
import json
import pathlib
import subprocess
import time

IMAGE = "sha256:0b0c246afeb8d343d25890bfe62d5f8af8aee528cb034a596733c3b338408327"
PREFIX = "cube-private-api-acceptance"
UNITS = ["cube-management-api.service", "cube-management-proxy.service"]
WORKER_SSH = ["ssh", "-i", "/opt/baarcha-cube/worker-01/operator-key", "-p", "20222",
              "-o", "StrictHostKeyChecking=yes", "-o", "UserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts",
              "-o", "BatchMode=yes", "root@127.0.0.1"]


def run(args, **kw):
    return subprocess.check_output(args, **kw)


def probe(port, authenticated=False):
    key = json.loads(run(WORKER_SSH + ["cat /root/cube-production/test-secrets.json"]))["cube_key"] if authenticated else ""
    assert "\r" not in key and "\n" not in key
    path = "/sandboxes?limit=1" if port == 20300 else "/healthz"
    headers = ("X-API-Key: " + key + "\r\n") if key else ""
    request = (f"GET {path} HTTP/1.1\r\nHost: cube-transport.invalid\r\n" + headers + "Connection: close\r\n\r\n").encode()
    # Private key rides stdin only, never argv, environment, logs or report.
    response = run(["docker", "exec", "-i", PREFIX, "/usr/bin/socat", "-t", "3", "-T", "5", "STDIO,ignoreeof",
                    f"TCP4:127.0.0.1:{port},connect-timeout=3"], input=request, timeout=15)
    return int(response.split(b"\r\n", 1)[0].split()[1])


def main():
    names = [PREFIX, PREFIX + "-api", PREFIX + "-proxy"]
    for name in names:
        if subprocess.run(["docker", "inspect", name], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0:
            raise RuntimeError("owned fixture name already exists; inspect before retry")
    initially_active = {u: subprocess.run(["systemctl", "is-active", "--quiet", u]).returncode == 0 for u in UNITS}
    created = []
    report = {"scope": "actual fresh Cube API via private UDS relays", "guest_mutations": 0, "production_controller_changed": False}
    try:
        subprocess.run(["systemctl", "start", *UNITS], check=True)
        common = ["docker", "run", "-d", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges:true",
                  "--user", "65532:65532", "--pids-limit", "270", "--memory", "192m", "--cpus", "0.5"]
        run(common + ["--name", PREFIX, "--network", "none", "--entrypoint", "/bin/sleep", IMAGE, "300"])
        created.append(PREFIX)
        for endpoint in ["api", "proxy"]:
            name = PREFIX + "-" + endpoint
            run(common + ["--name", name, "--network", "container:" + PREFIX, "--userns", "host", "--group-add", "982",
                          "--mount", "type=bind,source=/run/cube-management,target=/run/cube-management,readonly",
                          IMAGE, endpoint])
            created.append(name)
        deadline = time.monotonic() + 8
        while True:
            try:
                status = probe(20300)
                break
            except (subprocess.SubprocessError, ValueError, IndexError):
                if time.monotonic() >= deadline:
                    raise
                time.sleep(.2)
        assert status in (401, 403), "anonymous management API accepted"
        report["anonymous_api_status"] = status
        report["authenticated_api_status"] = probe(20300, True)
        assert report["authenticated_api_status"] == 200
        report["proxy_transport_status"] = probe(20080)
        assert report["proxy_transport_status"] in (200, 204, 400, 401, 403, 404)
        for endpoint in ["api", "proxy"]:
            run(["docker", "exec", PREFIX + "-" + endpoint, "/usr/local/bin/healthcheck.sh", endpoint], timeout=8)
        report["actual_api_and_proxy_healthchecks_passed"] = True
        report["relay_image_id"] = IMAGE
        namespaces = []
        for name in names:
            inspected = json.loads(run(["docker", "inspect", name]))[0]
            assert not inspected["HostConfig"]["PortBindings"]
            namespaces.append(str(pathlib.Path(f"/proc/{inspected['State']['Pid']}/ns/net").readlink()))
        assert len(set(namespaces)) == 1
        report["same_controller_namespace_no_published_ports"] = True
    finally:
        for name in reversed(created):
            subprocess.run(["docker", "rm", "-f", name], check=True, stdout=subprocess.DEVNULL)
        for unit, active in initially_active.items():
            if not active:
                subprocess.run(["systemctl", "stop", unit], check=True)
    report["owned_containers_removed"] = True
    report["host_units_restored_to_prior_activation"] = True
    print(json.dumps(report, indent=2))


if __name__ == "__main__":
    main()
