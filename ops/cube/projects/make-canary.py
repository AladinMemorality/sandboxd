#!/usr/bin/env python3
"""Create secret-free source fixtures, never read customer files."""
import json
import sys
import zipfile
from pathlib import Path

out = Path(sys.argv[1])
out.mkdir(mode=0o700)
files = {
    "index.html": "<!doctype html><title>Cube deployment canary</title><h1>Portable project revision</h1>",
    "sandbox.yaml": "version: 1\nweb:\n  command: python3 -m http.server 3000\n  port: 3000\n  health_path: /\n",
    "node_modules/disposable/index.js": "this dependency directory must not enter S3",
}
with zipfile.ZipFile(out / "input.zip", "w", compression=zipfile.ZIP_DEFLATED) as archive:
    for name, body in files.items():
        archive.writestr(name, body)
(out / "input.zip").chmod(0o600)
recipe = {"version": 1, "runtime_image": "example/canary@sha256:" + "a" * 64,
          "package_manager": "none", "start": ["sh", "-lc", "python3 -m http.server 3000"],
          "port": 3000, "health_path": "/"}
(out / "recipe.json").write_text(json.dumps(recipe))
(out / "recipe.json").chmod(0o600)
