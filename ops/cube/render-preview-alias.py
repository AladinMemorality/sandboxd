#!/usr/bin/env python3
"""Render a reviewed public app alias through sandboxd's provider-aware preview.

Read-only: emits JSON (valid YAML) for Traefik's file provider. Does not install
configuration, change app visibility, or bypass private-preview authorization.
"""
import argparse
import json
import re
import sqlite3
from pathlib import Path


def hostname(value):
    value = value.lower()
    if len(value) > 253 or not all(
        re.fullmatch(r"[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?", part)
        for part in value.split(".")
    ):
        raise ValueError("expected a DNS hostname without scheme, port or wildcard")
    return value


def render(db, sandbox_id, alias, preview_domain):
    if not re.fullmatch(r"[0-9A-HJKMNP-TV-Z]{26}", sandbox_id):
        raise ValueError("expected a canonical sandbox ID")
    alias, preview_domain = hostname(alias), hostname(preview_domain)
    row = db.execute(
        "SELECT visibility,web_port FROM sandbox WHERE id=?", (sandbox_id,)
    ).fetchone()
    if row is None or row[0] != "public":
        raise ValueError("alias requires an existing runtime-public app; private aliases need separate owner handoff support")
    port = row[1]
    if not isinstance(port, int) or not 1 <= port <= 65535 or port in (3031, 49983):
        raise ValueError("invalid application web port")
    if db.execute("SELECT 1 FROM sandbox_port WHERE sandbox_id=? AND port=?", (sandbox_id, port)).fetchone() is None:
        raise ValueError("web port is not exposed by this sandbox")
    canonical = f"s-{sandbox_id.lower()}-{port}.preview.{preview_domain}"
    if alias == canonical or alias.startswith("s-") or alias.startswith("api."):
        raise ValueError("alias must not overlap canonical preview or API hostnames")
    name = "app-alias-" + sandbox_id.lower()
    return {"http": {
        "routers": {name: {
            "rule": f"Host(`{alias}`)", "entryPoints": ["web"], "priority": 200,
            "service": "sandbox-wake@file", "middlewares": [name + "@file", "sandbox-preview-embed@file"],
        }},
        "middlewares": {name: {"headers": {"customRequestHeaders": {
            "Host": canonical,
        }}}},
    }}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--database", required=True)
    parser.add_argument("--sandbox", required=True)
    parser.add_argument("--alias", required=True)
    parser.add_argument("--preview-domain", required=True)
    args = parser.parse_args()
    with sqlite3.connect(Path(args.database).resolve().as_uri() + "?mode=ro", uri=True) as db:
        print(json.dumps(render(db, args.sandbox, args.alias, args.preview_domain), indent=2))


if __name__ == "__main__":
    main()
