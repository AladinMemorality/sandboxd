#!/usr/bin/env python3
"""Read-only project/source location inventory; never wakes guests or reads secrets."""
import argparse
import json
import sqlite3
from pathlib import Path


def inventory(database: Path, worker_id: str):
    with sqlite3.connect(database.resolve().as_uri() + "?mode=ro", uri=True) as db:
        db.execute("PRAGMA query_only=ON")
        db.row_factory = sqlite3.Row
        rows = db.execute("""
            SELECT a.id AS app_id, a.name, a.external_user_id AS creator_id,
                   a.external_project_id AS project_id,
                   s.id AS sandbox_id, s.status, s.runtime_provider,
                   b.runtime_id, b.template_id
            FROM app a
            LEFT JOIN sandbox s ON s.id=(
                SELECT id FROM sandbox WHERE app_id=a.id ORDER BY created_at DESC, id DESC LIMIT 1
            )
            LEFT JOIN runtime_binding b ON b.sandbox_id=s.id
            ORDER BY a.created_at, a.id
        """).fetchall()
        result = []
        for row in rows:
            item = dict(row)
            cube = item["runtime_provider"] == "cube"
            item.update(worker_id=worker_id if cube else None,
                        worker_assignment_source="operator_single_worker_inventory" if cube else None,
                        guest_source_path="/home/sandbox/workspace/app" if cube else None,
                        portable_deployment_eligible=False,
                        eligibility_reason="Source, dependency and persistent-data inventory not yet verified")
            result.append(item)
        return {"version": 1, "read_only": True, "projects": result}


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("database", type=Path)
    parser.add_argument("--single-worker-id", required=True)
    args = parser.parse_args()
    print(json.dumps(inventory(args.database, args.single_worker_id), indent=2))
