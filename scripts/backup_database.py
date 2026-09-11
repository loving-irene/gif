#!/usr/bin/env python3
"""部署前生成一致的SQLite快照，只保留最近两份本项目自动备份。"""

import argparse
from contextlib import closing
from datetime import datetime, timezone
import json
import math
import os
from pathlib import Path
import re
import sqlite3
import sys
import tempfile
import time
import uuid


AUTO_BACKUP = re.compile(r"gif-auto-\d{8}T\d{12}Z-[0-9a-f]{8}\.sqlite3\Z")


def database_path(app_dir, config_file, environment):
    # 与应用一致读取.env的键值，不通过shell执行配置文件。
    values = {}
    for line in config_file.read_text(encoding="utf-8-sig").splitlines():
        line = line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, value = line.split("=", 1)
        values[key.strip()] = value.strip().strip("\"'")
    value = environment.get("GIF_DATABASE_PATH", values.get("GIF_DATABASE_PATH", "gif.db"))
    if not value:
        raise ValueError("GIF_DATABASE_PATH must not be empty")
    path = Path(value)
    return (path if path.is_absolute() else app_dir / path).resolve()


def create_snapshot(source, destination, timeout):
    deadline = time.monotonic() + timeout

    def progress(status, remaining, total):
        if time.monotonic() >= deadline:
            raise TimeoutError("database backup exceeded its time limit")

    with closing(sqlite3.connect(source.as_uri() + "?mode=ro", uri=True, timeout=5)) as src:
        with closing(sqlite3.connect(str(destination), timeout=5)) as dst:
            src.backup(dst, pages=128, progress=progress, sleep=0.1)
            dst.execute("PRAGMA journal_mode=DELETE")
            if dst.execute("PRAGMA quick_check").fetchall() != [("ok",)]:
                raise RuntimeError("database backup integrity check failed")
    with destination.open("r+b") as stream:
        os.fsync(stream.fileno())


def prune_backups(directory, newest):
    candidates = [
        path for path in directory.iterdir()
        if AUTO_BACKUP.fullmatch(path.name) and not path.is_symlink() and path.is_file()
    ]
    # 始终保留刚生成的快照，避免系统时钟调整使新快照被误清理。
    candidates.sort(key=lambda p: (p == newest, p.stat().st_mtime_ns, p.name), reverse=True)
    removed = []
    for path in candidates[2:]:
        if path.parent.resolve() != directory or path.is_symlink():
            raise RuntimeError("backup cleanup path changed")
        path.unlink()
        removed.append(path.name)
    return removed


def backup_database(app_dir, config_file, backup_dir, timeout=60, environment=None):
    if not math.isfinite(timeout) or timeout <= 0:
        raise ValueError("backup timeout must be positive and finite")
    app_dir = Path(app_dir).resolve()
    source = database_path(app_dir, Path(config_file), os.environ if environment is None else environment)
    if not source.exists():
        return {"event": "database_backup_skipped", "reason": "database does not exist", "database": str(source)}
    if not source.is_file():
        raise ValueError("database path is not a regular file")
    requested_dir = Path(backup_dir)
    if requested_dir.is_symlink():
        raise ValueError("backup directory must not be a symbolic link")
    backup_dir = requested_dir.resolve()
    if backup_dir == app_dir or backup_dir in app_dir.parents or backup_dir in source.parents:
        raise ValueError("use a dedicated backup subdirectory")
    backup_dir.mkdir(mode=0o700, parents=True, exist_ok=True)
    stamp = datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%S%fZ")
    destination = backup_dir / ("gif-auto-" + stamp + "-" + uuid.uuid4().hex[:8] + ".sqlite3")
    fd, temporary = tempfile.mkstemp(prefix=".gif-backup-", suffix=".partial", dir=str(backup_dir))
    os.close(fd)
    temporary = Path(temporary)
    try:
        create_snapshot(source, temporary, timeout)
        os.replace(str(temporary), str(destination))
    finally:
        if temporary.exists():
            temporary.unlink()
    removed = prune_backups(backup_dir, destination)
    return {"event": "database_backup_complete", "database": str(source), "backup": str(destination), "keep": 2, "removed": removed}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--app-dir", default="/var/www/gif")
    parser.add_argument("--config")
    parser.add_argument("--backup-dir", default="/var/backups/gif")
    parser.add_argument("--timeout", type=float, default=60)
    args = parser.parse_args()
    os.umask(0o077)
    try:
        result = backup_database(args.app_dir, args.config or str(Path(args.app_dir) / ".env"), args.backup_dir, args.timeout)
        print(json.dumps(result, ensure_ascii=False))
    except (OSError, sqlite3.Error, ValueError, RuntimeError, TimeoutError) as error:
        print(json.dumps({"event": "database_backup_failed", "error": str(error)}, ensure_ascii=False), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
