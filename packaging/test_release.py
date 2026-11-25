"""Exercise release provenance, bundle corruption and the local staging CLI."""

import hashlib
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

import package
import release

TAG = "v0.1.0-alpha.1"
COMMIT = "a" * 40


class ReleaseBundleTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix="shadowtalk-release-test-")
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.bundle = self.root / "сборка with spaces"
        self.output = self.root / "staged"
        self.bundle.mkdir()
        # Match the published matrix independently of the manifest generator.
        targets = {
            "darwin_arm64": ("tar.gz", "pkg", "dmg"),
            "darwin_amd64": ("tar.gz", "pkg", "dmg"),
            "linux_arm64": ("tar.gz", "deb", "rpm"),
            "linux_amd64": ("tar.gz", "deb", "rpm"),
            "windows_amd64": ("zip",),
        }
        for target, extensions in targets.items():
            for extension in extensions:
                (self.bundle / f"shadowtalk_0.1.0-alpha.1_{target}.{extension}").write_bytes(b"fixture payload")
        (self.bundle / "shadowtalk_0.1.0-alpha.1_windows_amd64_setup.exe").write_bytes(b"fixture setup")
        self.metadata = self.bundle / "metadata.json"
        self.metadata.write_text(json.dumps(dict(
            project_name="shadowtalk", tag=TAG, version=TAG[1:], commit=COMMIT,
        )), encoding="utf-8")
        cask = self.bundle / "homebrew/Casks/shadowtalk.rb"
        cask.parent.mkdir(parents=True)
        cask.write_text("fixture cask", encoding="utf-8")
        (self.bundle / "THIRD_PARTY_NOTICES.txt").write_text("fixture notices", encoding="utf-8")
        package.checksums(self.bundle)

    def prepare(self):
        release.prepare(release.ROOT, self.bundle, self.output, TAG, COMMIT)

    def test_cli_stages_complete_flat_assets_with_matching_checksums(self):
        result = subprocess.run([
            sys.executable, str(Path(release.__file__)), "prepare", "--tag", TAG,
            "--commit", COMMIT, "--bundle", str(self.bundle), "--output", str(self.output),
        ], capture_output=True, text=True, encoding="utf-8", timeout=10)
        self.assertEqual(result.returncode, 0, result.stderr)
        assets = self.output / "assets"
        self.assertEqual(len(list(assets.iterdir())), 18)
        names = set()
        for line in (assets / "checksums.txt").read_text().splitlines():
            checksum, name = line.split("  ")
            self.assertNotIn("/", name)
            self.assertEqual(checksum, hashlib.sha256((assets / name).read_bytes()).hexdigest())
            names.add(name)
        self.assertEqual(names, {p.name for p in assets.iterdir()} - {"checksums.txt"})
        self.assertIn(COMMIT, (self.output / "release-notes.md").read_text())
        self.assertEqual((assets / "shadowtalk.rb").read_bytes(),
                         (self.bundle / "homebrew/Casks/shadowtalk.rb").read_bytes())
        self.assertEqual((assets / "THIRD_PARTY_NOTICES.txt").read_bytes(),
                         (self.bundle / "THIRD_PARTY_NOTICES.txt").read_bytes())

    def test_rejects_bundle_without_third_party_notices(self):
        (self.bundle / "THIRD_PARTY_NOTICES.txt").unlink()
        package.checksums(self.bundle)
        with self.assertRaisesRegex(ValueError, "missing=.*THIRD_PARTY_NOTICES"):
            self.prepare()
        self.assertFalse(self.output.exists())

    def test_rejects_snapshot_wrong_tag_or_commit_before_creating_output(self):
        original = self.metadata.read_text()
        for key, value in (("version", "0.1.0-snapshot.aaaaaaa"),
                           ("tag", "v0.1.0-alpha.2"), ("commit", "b" * 40)):
            with self.subTest(key=key):
                metadata = json.loads(original)
                metadata[key] = value
                self.metadata.write_text(json.dumps(metadata))
                package.checksums(self.bundle)
                with self.assertRaisesRegex(ValueError, "metadata"):
                    self.prepare()
                self.assertFalse(self.output.exists())

    def test_rejects_missing_installer_and_unexpected_private_file(self):
        installer = self.bundle / "shadowtalk_0.1.0-alpha.1_windows_amd64_setup.exe"
        payload = installer.read_bytes()
        installer.unlink()
        with self.assertRaisesRegex(ValueError, "missing=.*setup.exe"):
            self.prepare()
        installer.write_bytes(payload)
        (self.bundle / "tor.json").write_text("private config")
        package.checksums(self.bundle)
        with self.assertRaisesRegex(ValueError, "extra=.*tor.json"):
            self.prepare()
        self.assertFalse(self.output.exists())

    def test_rejects_corruption_and_stale_checksums(self):
        cask = self.bundle / "homebrew/Casks/shadowtalk.rb"
        cask.write_text("changed after checksumming")
        with self.assertRaisesRegex(ValueError, "corrupted"):
            self.prepare()
        self.assertFalse(self.output.exists())

    def test_rejects_duplicate_missing_or_traversing_checksum_entries(self):
        path = self.bundle / "checksums.txt"
        original = path.read_text()
        lines = original.splitlines(keepends=True)
        for manifest in (original + lines[0], "".join(lines[1:]),
                         original + "0" * 64 + "  ../private\n"):
            with self.subTest(manifest=manifest[-90:]):
                path.write_text(manifest)
                with self.assertRaises(ValueError):
                    self.prepare()
                self.assertFalse(self.output.exists())

    @unittest.skipIf(sys.platform == "win32", "symlink creation may require elevated rights")
    def test_rejects_symlink_even_with_matching_checksum(self):
        cask = self.bundle / "homebrew/Casks/shadowtalk.rb"
        external = self.root / "external"
        external.write_bytes(cask.read_bytes())
        cask.unlink()
        cask.symlink_to(external)
        with self.assertRaisesRegex(ValueError, "symlinks"):
            self.prepare()

    def test_never_reuses_existing_output(self):
        self.output.mkdir()
        marker = self.output / "keep"
        marker.write_text("previous release")
        with self.assertRaisesRegex(ValueError, "output already exists"):
            self.prepare()
        self.assertEqual(marker.read_text(), "previous release")

    def test_cli_rejects_non_alpha_tag_without_writing(self):
        for tag in ("v0.1.0", "v0.1.0-alpha.0", "v01.1.0-alpha.1", "v0.1.0-alpha.1/../../x"):
            with self.subTest(tag=tag):
                result = subprocess.run([
                    sys.executable, str(Path(release.__file__)), "prepare", "--tag", tag,
                    "--commit", COMMIT, "--bundle", str(self.bundle), "--output", str(self.output),
                ], capture_output=True, text=True, timeout=10)
                self.assertEqual(result.returncode, 1)
                self.assertNotIn("Traceback", result.stderr)
                self.assertFalse(self.output.exists())


class ReleaseSourceTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix="shadowtalk-release-git-")
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.git("init", "-q", "--initial-branch=main")
        self.git("config", "user.name", "Release Test")
        self.git("config", "user.email", "release-test@example.invalid")
        self.git("config", "commit.gpgsign", "false")
        self.git("config", "tag.gpgsign", "false")
        notes = self.root / "docs/releases" / f"{TAG}.md"
        notes.parent.mkdir(parents=True)
        notes.write_text("Test alpha notes")
        self.git("add", ".")
        self.git("commit", "-qm", "fixture")
        self.commit = self.git("rev-parse", "HEAD")
        self.git("update-ref", "refs/remotes/origin/main", self.commit)
        self.git("tag", "-a", TAG, "-m", "fixture alpha")

    def git(self, *args):
        return release.git(self.root, *args)

    def test_accepts_annotated_tag_on_clean_main(self):
        release.check_source(self.root, TAG, self.commit)

    def test_rejects_feature_commit_not_merged_into_main(self):
        (self.root / "feature.txt").write_text("not merged")
        self.git("add", ".")
        self.git("commit", "-qm", "feature")
        head = self.git("rev-parse", "HEAD")
        self.git("tag", "-f", TAG, head)
        with self.assertRaisesRegex(ValueError, "merged into origin/main"):
            release.check_source(self.root, TAG, head)

    def test_rejects_moved_tag_or_dirty_source(self):
        self.git("commit", "--allow-empty", "-qm", "next")
        head = self.git("rev-parse", "HEAD")
        self.git("update-ref", "refs/remotes/origin/main", head)
        with self.assertRaisesRegex(ValueError, "tag does not point"):
            release.check_source(self.root, TAG, head)
        self.git("tag", "-f", TAG, head)
        (self.root / "untracked.go").write_text("uncommitted")
        with self.assertRaisesRegex(ValueError, "must be clean"):
            release.check_source(self.root, TAG, head)


if __name__ == "__main__":
    unittest.main()
