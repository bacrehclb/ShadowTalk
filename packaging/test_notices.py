"""Guard license coverage across platforms and fail closed on incomplete inputs."""

import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import notices


class NoticeTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix="shadowtalk-notices-test-")
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.goroot = self.root / "Go toolchain"
        self.module = self.root / "module cache"
        self.nested = self.module / "internal/quoted"
        self.nested.mkdir(parents=True)
        (self.goroot / "src/runtime").mkdir(parents=True)
        (self.goroot / "LICENSE").write_bytes(b"Go license fixture\n")
        (self.module / "LICENSE").write_bytes(b"Module license fixture\r\n")
        (self.module / "PATENTS").write_bytes(b"Patent grant fixture\n")
        (self.nested / "LICENSE.txt").write_bytes("Nested copyright © example\n".encode())
        (self.module / "testdata/unused").mkdir(parents=True)
        (self.module / "testdata/unused/LICENSE").write_bytes(b"UNUSED TEST NOTICE")
        supplement = self.root / "packaging/licenses"
        supplement.mkdir(parents=True)
        (supplement / "Unicode-3.0.txt").write_bytes(b"Unicode fixture\n")

    def module_package(self, directory, version="v1.2.3"):
        return dict(ImportPath="example.org/dependency", Dir=str(directory), Module=dict(
            Path="example.org/dependency", Version=version, Dir=str(self.module)))

    def fake_go(self, root, env, *args):
        if args[0] == "env":
            return json.dumps(dict(GOROOT=str(self.goroot), GOVERSION="go1.26.3"))
        self.assertEqual(args, ("list", "-mod=readonly", "-deps", "-json", "./cmd/shadowtalk"))
        self.assertEqual((env["CGO_ENABLED"], env["GOWORK"], env["GOFLAGS"]), ("0", "off", ""))
        packages = [dict(Standard=True, Dir=str(self.goroot / "src/runtime")),
                    dict(Module=dict(Main=True), Dir=str(root)),
                    self.module_package(self.module)]
        # Only the Windows build imports this separately licensed nested package.
        if env["GOOS"] == "windows":
            packages.append(self.module_package(self.nested))
        return "\n\n".join(json.dumps(p) for p in packages) + "\n"

    def test_all_targets_include_nested_notices_and_preserve_original_bytes(self):
        with patch.object(notices, "go", side_effect=self.fake_go) as go:
            data = notices.generate(self.root)
            targets = {(c.args[1]["GOOS"], c.args[1]["GOARCH"])
                       for c in go.call_args_list if c.args[2] == "list"}
            self.assertEqual(targets, {("darwin", "arm64"), ("darwin", "amd64"),
                                      ("linux", "arm64"), ("linux", "amd64"), ("windows", "amd64")})
            self.assertEqual(data, notices.generate(self.root))
        self.assertEqual(data.count(b"Module: example.org/dependency@v1.2.3\n"), 1)
        for path in (self.module / "LICENSE", self.module / "PATENTS", self.nested / "LICENSE.txt"):
            self.assertIn(path.read_bytes(), data)
        self.assertIn(b"Unicode fixture", data)
        self.assertNotIn(b"UNUSED TEST NOTICE", data)
        self.assertNotIn(str(self.root).encode(), data)

    def test_generation_failure_preserves_existing_output(self):
        output = self.root / "output.txt"
        output.write_bytes(b"previous complete report")
        with patch.object(notices, "go", side_effect=self.fake_go), patch(
                "sys.argv", ["notices.py", "--output", str(output)]), patch.object(
                notices, "ROOT", self.root):
            # An empty license must fail even if other notices exist.
            (self.nested / "LICENSE.txt").write_bytes(b" ")
            with self.assertRaises(SystemExit) as raised:
                notices.main()
        self.assertEqual(raised.exception.code, 1)
        self.assertEqual(output.read_bytes(), b"previous complete report")

    def test_patents_alone_do_not_satisfy_missing_license(self):
        (self.module / "LICENSE").unlink()
        with self.assertRaisesRegex(ValueError, "no license text"):
            notices.license_paths(self.module, [self.module])

    def test_replaced_and_inconsistent_modules_require_review(self):
        item = self.module_package(self.module)
        item["Module"]["Replace"] = dict(Dir="/unreviewed")
        with self.assertRaisesRegex(ValueError, "replaced module"):
            notices.collect([item], {}, set())
        with self.assertRaisesRegex(ValueError, "inconsistent module"):
            notices.collect([self.module_package(self.module),
                             self.module_package(self.module, "v2.0.0")], {}, set())


if __name__ == "__main__":
    unittest.main()
