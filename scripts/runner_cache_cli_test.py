import tempfile
import unittest
from pathlib import Path
import sys

sys.path.insert(0, str(Path(__file__).resolve().parent))
import runner_cache_cli


class RunnerCacheCLITest(unittest.TestCase):
    def test_collect_reports_size_and_file_count(self):
        with tempfile.TemporaryDirectory(prefix="packaging-cache-") as cache_dir:
            root = Path(cache_dir)
            (root / "bun").mkdir()
            (root / "bun" / "a.bin").write_bytes(b"abc")
            (root / "bun" / "b.bin").write_bytes(b"defg")

            entries = runner_cache_cli.collect([("bun", str(root / "bun"))], limit=10)

            self.assertEqual(len(entries), 1)
            self.assertTrue(entries[0].exists)
            self.assertEqual(entries[0].files, 2)
            self.assertEqual(entries[0].bytes, 7)

    def test_cleanup_is_dry_run_by_default(self):
        with tempfile.TemporaryDirectory(prefix="packaging-cache-") as cache_dir:
            root = Path(cache_dir)
            (root / "artifact.tmp").write_text("keep until apply", encoding="utf-8")
            entries = runner_cache_cli.collect([("artifacts", str(root))], limit=10)

            cleaned = runner_cache_cli.cleanup_entries(entries, apply=False)

            self.assertEqual(len(cleaned), 1)
            self.assertTrue((root / "artifact.tmp").exists())

    def test_cleanup_apply_removes_children_not_root(self):
        with tempfile.TemporaryDirectory(prefix="packaging-cache-") as cache_dir:
            root = Path(cache_dir)
            (root / "artifact.tmp").write_text("remove", encoding="utf-8")
            entries = runner_cache_cli.collect([("artifacts", str(root))], limit=10)

            cleaned = runner_cache_cli.cleanup_entries(entries, apply=True)

            self.assertEqual(len(cleaned), 1)
            self.assertTrue(root.exists())
            self.assertFalse((root / "artifact.tmp").exists())

    def test_cleanup_refuses_non_cache_paths(self):
        with tempfile.TemporaryDirectory(prefix="ordinary-") as cache_dir:
            root = Path(cache_dir)
            (root / "artifact.tmp").write_text("keep", encoding="utf-8")
            entries = runner_cache_cli.collect([("bad", str(root))], limit=10)

            with self.assertRaises(ValueError):
                runner_cache_cli.cleanup_entries(entries, apply=True)


if __name__ == "__main__":
    unittest.main()
