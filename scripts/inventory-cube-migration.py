#!/usr/bin/env python3
"""Read-only, metadata-only legacy inventory. No contents/config secrets read."""
import argparse
import collections
import datetime
import json
import os
import sqlite3
import stat

parser = argparse.ArgumentParser()
parser.add_argument("--database", default="/var/lib/sandboxd/state/sandboxd.db")
parser.add_argument("--workspaces", default="/var/lib/sandboxd/workspaces")
args = parser.parse_args()
db = sqlite3.connect("file:" + args.database + "?mode=ro", uri=True)
apps = []
for sid, status, home, preset, webport in db.execute(
    "SELECT s.id,s.status,s.workspace_mnt,COALESCE(a.runtime_preset,''),s.web_port "
    "FROM sandbox s LEFT JOIN app a ON a.id=s.app_id ORDER BY s.id"
):
    if home != os.path.join(args.workspaces, sid):
        apps.append({"sandbox_id": sid, "storage_layout_supported": False})
        continue
    item = {"sandbox_id": sid, "status": status, "runtime_preset": preset,
            "web_port": webport, "storage_layout_supported": True,
            "app_bytes": 0, "app_regular_files": 0, "app_hardlinked_files": 0,
            "app_special_files": 0, "app_largest_file_bytes": 0}
    task_ids = [row[0] for row in db.execute("SELECT task_id FROM task WHERE sandbox_id=?", (sid,))]
    item["canonical_tasks"] = len(task_ids)
    item["tasks_missing_event_or_result_file"] = sum(
        not all(os.path.isfile(os.path.join(home, ".runtimed", "tasks", task_id, name))
                for name in ("events.jsonl", "result.json"))
        for task_id in task_ids
    )
    home_counts, home_bytes = collections.Counter(), collections.Counter()
    for directory, dirs, files in os.walk(home, followlinks=False):
        for name in files:
            path = os.path.join(directory, name)
            try:
                info = os.lstat(path)
            except FileNotFoundError:
                continue  # Live inventory; definitive check is after quiescence.
            relative = os.path.relpath(path, home)
            if relative.startswith("workspace/app/"):
                if stat.S_ISREG(info.st_mode):
                    item["app_bytes"] += info.st_size
                    item["app_regular_files"] += 1
                    item["app_largest_file_bytes"] = max(item["app_largest_file_bytes"], info.st_size)
                    item["app_hardlinked_files"] += int(info.st_nlink > 1)
                elif not stat.S_ISLNK(info.st_mode):
                    item["app_special_files"] += 1
            else:
                category = relative.split("/")[0]
                home_counts[category] += 1
                if stat.S_ISREG(info.st_mode):
                    home_bytes[category] += info.st_size
    item["home_categories"] = dict(sorted(home_counts.items()))
    item["home_bytes_by_category"] = dict(sorted(home_bytes.items()))
    apps.append(item)
print(json.dumps({"observed_at": datetime.datetime.now(datetime.timezone.utc).isoformat(),
                  "method": "SQLite mode=ro and filesystem lstat; no file contents or decrypted configuration",
                  "active_coding_tasks": db.execute("SELECT COUNT(*) FROM task WHERE status='running'").fetchone()[0],
                  "sandbox_count": len(apps), "apps": apps}, indent=2, sort_keys=True))
