import importlib.util
from contextlib import closing
import os
from pathlib import Path
import sqlite3
import subprocess
import tempfile
import unittest
from unittest.mock import patch


ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location("backup_database", ROOT / "scripts/backup_database.py")
backup = importlib.util.module_from_spec(spec)
spec.loader.exec_module(backup)


class DatabaseBackupTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="gif-backup-test-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.app = self.root / "app"
        self.app.mkdir()
        self.config = self.app / ".env"
        self.config.write_text("GIF_DATABASE_PATH='live.db'\n", encoding="utf-8")
        self.source = self.app / "live.db"
        self.destination = self.root / "backups"

    def run_backup(self):
        return backup.backup_database(self.app, self.config, self.destination, environment={})

    def test_wal_data_is_included_and_only_two_auto_backups_remain(self):
        self.destination.mkdir()
        manual = self.destination / "manual-important.sqlite3"
        manual.write_bytes(b"keep manual backup")
        unrelated = self.destination / "gif-auto-not-a-snapshot.sqlite3"
        unrelated.write_bytes(b"keep unrelated file")
        with closing(sqlite3.connect(self.source)) as writer:
            writer.execute("PRAGMA journal_mode=WAL")
            writer.execute("PRAGMA wal_autocheckpoint=0")
            writer.execute("CREATE TABLE sample(value INTEGER)")
            writer.commit()
            for value in range(5):
                writer.execute("INSERT INTO sample VALUES(?)", (value,))
                writer.commit()
                self.assertGreater(Path(str(self.source) + "-wal").stat().st_size, 0)
                result = self.run_backup()
                with closing(sqlite3.connect(result["backup"])) as snapshot:
                    self.assertEqual(snapshot.execute("SELECT MAX(value) FROM sample").fetchone(), (value,))
                    self.assertEqual(snapshot.execute("PRAGMA quick_check").fetchone(), ("ok",))
                    self.assertEqual(snapshot.execute("PRAGMA journal_mode").fetchone(), ("delete",))
        automatic = [p for p in self.destination.iterdir() if backup.AUTO_BACKUP.fullmatch(p.name)]
        self.assertEqual(len(automatic), 2)
        values = []
        for file in automatic:
            with closing(sqlite3.connect(file)) as snapshot:
                values.append(snapshot.execute("SELECT MAX(value) FROM sample").fetchone()[0])
        self.assertEqual(sorted(values), [3, 4])
        self.assertEqual(manual.read_bytes(), b"keep manual backup")
        self.assertEqual(unrelated.read_bytes(), b"keep unrelated file")

    def test_failure_never_prunes_prior_backups(self):
        with closing(sqlite3.connect(self.source)) as db:
            db.execute("CREATE TABLE sample(value INTEGER)")
            db.commit()
        self.run_backup()
        self.run_backup()
        before = {p.name: p.read_bytes() for p in self.destination.iterdir()}
        with patch.object(backup, "create_snapshot", side_effect=sqlite3.DatabaseError("simulated failure")):
            with self.assertRaises(sqlite3.DatabaseError):
                self.run_backup()
        after = {p.name: p.read_bytes() for p in self.destination.iterdir()}
        self.assertEqual(before, after)

    def test_initial_missing_database_does_not_create_or_prune(self):
        result = self.run_backup()
        self.assertEqual(result["event"], "database_backup_skipped")
        self.assertFalse(self.source.exists())
        self.assertFalse(self.destination.exists())

    def test_config_is_not_executed_and_environment_override_is_supported(self):
        self.config.write_text("IGNORED=$(echo unsafe)\nGIF_DATABASE_PATH=\"data/original #1.db\"\n", encoding="utf-8")
        self.assertEqual(backup.database_path(self.app, self.config, {}), (self.app / "data/original #1.db").resolve())
        self.assertEqual(backup.database_path(self.app, self.config, {"GIF_DATABASE_PATH": "alternate.db"}), (self.app / "alternate.db").resolve())


class DeploymentBackupHookTests(unittest.TestCase):
    def test_backup_failure_blocks_binary_swap_and_service_restart(self):
        bash = os.environ.get("GIF_TEST_BASH", "bash")
        with tempfile.TemporaryDirectory(prefix="gif-deploy-backup-") as directory:
            root = Path(directory)
            app = root / "app"
            bin_dir = root / "bin"
            app.mkdir()
            bin_dir.mkdir()
            (app / ".env").write_text("GIF_DATABASE_PATH=live.db\n", encoding="utf-8")
            (app / "gif-server").write_text("previous binary", encoding="utf-8")
            events = root / "events"

            def shell_path(path):
                if os.name != "nt":
                    return str(path)
                return subprocess.check_output([bash, "-c", 'cygpath -u "$1"', "--", str(path)], text=True).strip()

            stub = '''#!/usr/bin/env bash
set -eu
case "$(basename "$0")" in
  go) printf 'go_%s\\n' "$1" >> "$BACKUP_TEST_EVENTS" ;;
  python3) printf 'backup\\n' >> "$BACKUP_TEST_EVENTS"; exit "$BACKUP_TEST_FAIL" ;;
  sudo)
    case "$1" in
      python3|systemctl) exec "$@" ;;
      tee) cat >/dev/null ;;
      install|chown|chmod) : ;;
      *) exit 99 ;;
    esac ;;
  systemctl) if [ "$1" = restart ]; then printf 'restart\\n' >> "$BACKUP_TEST_EVENTS"; fi ;;
  mv) printf 'swap\\n' >> "$BACKUP_TEST_EVENTS" ;;
  curl) printf 'ok\\n' ;;
  git) printf 'abcdef0\\n' ;;
  ss|flock|chmod|cp) : ;;
esac
'''
            for name in ["go", "python3", "sudo", "systemctl", "mv", "curl", "git", "ss", "flock", "chmod", "cp"]:
                file = bin_dir / name
                with file.open("w", encoding="utf-8", newline="\n") as out:
                    out.write(stub)
                file.chmod(0o755)
            env = dict(os.environ, APP_DIR=shell_path(app), DATA_DIR=shell_path(root / "data"), BACKUP_DIR=shell_path(root / "backups"), BACKUP_TEST_BIN=shell_path(bin_dir), BACKUP_TEST_EVENTS=shell_path(events), BACKUP_TEST_SCRIPT=shell_path(ROOT / "scripts/deploy.sh"))
            for failure in ["23", "0"]:
                events.write_text("", encoding="utf-8")
                env["BACKUP_TEST_FAIL"] = failure
                run = subprocess.run([bash, "-lc", 'export PATH="$BACKUP_TEST_BIN:$PATH"; exec bash "$BACKUP_TEST_SCRIPT"'], env=env, capture_output=True, text=True, timeout=20)
                lines = events.read_text(encoding="utf-8").splitlines()
                self.assertIn("backup", lines, run.stderr)
                if failure != "0":
                    self.assertNotEqual(run.returncode, 0)
                    self.assertNotIn("swap", lines)
                    self.assertNotIn("restart", lines)
                else:
                    self.assertEqual(run.returncode, 0, run.stderr)
                    self.assertLess(lines.index("backup"), lines.index("swap"))
                    self.assertLess(lines.index("swap"), lines.index("restart"))


if __name__ == "__main__":
    unittest.main()
