"""HTTP-level regression coverage for preview path-injection alerts #47/#48."""
import importlib.util
import io
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

SPEC = importlib.util.spec_from_file_location("docs_build", Path(__file__).with_name("build.py"))
build = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(build)


class RequestSocket:
    """Exercise BaseHTTPRequestHandler's real HTTP parser without a listener."""
    def __init__(self, target):
        self.input = io.BytesIO(("GET " + target + " HTTP/1.0\r\n\r\n").encode("ascii"))
        self.output = bytearray()

    def makefile(self, *args):
        return self.input

    def sendall(self, data):
        self.output.extend(data)


class PreviewTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name) / "docs"
        (self.root / "img" / "nested").mkdir(parents=True)
        (self.root / "main.md").write_text("---\nslug: /\ntitle: Preview\n---\n# Safe page\n")
        (self.root / "secret.txt").write_bytes(b"SECRET OUTSIDE IMAGES")
        (self.root / "img" / "logo.png").write_bytes(b"PNG ASSET")
        (self.root / "img" / "nested" / "space image.jpg").write_bytes(b"JPEG ASSET")
        (self.root / "img" / "extra.bin").write_bytes(b"BINARY ASSET")
        outside = Path(self.temp.name) / "outside"
        outside.mkdir()
        (outside / "private.png").write_bytes(b"SECRET OUTSIDE DOCS")
        (self.root / "img" / "escape.png").symlink_to(outside / "private.png")
        (self.root / "img" / "escape-dir").symlink_to(outside, target_is_directory=True)
        patcher = patch.object(build, "ROOT", str(self.root))
        patcher.start()
        self.addCleanup(patcher.stop)
        self.handler = build.preview_handler()

    def request(self, target):
        connection = RequestSocket(target)
        self.handler(connection, ("127.0.0.1", 12345), None)
        headers, body = bytes(connection.output).split(b"\r\n\r\n", 1)
        return int(headers.split()[1]), headers, body

    def test_regular_nested_and_encoded_images(self):
        for path, expected, mime in [
            ("/img/logo.png?version=2", b"PNG ASSET", b"image/png"),
            ("/img/nested/space%20image.jpg", b"JPEG ASSET", b"image/jpeg"),
            ("/img/extra.bin", b"BINARY ASSET", b"application/octet-stream"),
        ]:
            with self.subTest(path=path):
                status, headers, body = self.request(path)
                self.assertEqual(status, 200)
                self.assertEqual(body, expected)
                self.assertIn(b"Content-Type: " + mime, headers)
                self.assertIn(b"Content-Length: " + str(len(body)).encode(), headers)

    def test_traversal_and_unknown_paths_are_not_served(self):
        for path in [
            "/img/../secret.txt", "/img/../../outside/private.png",
            "/img/%2e%2e/secret.txt", "/img/..%2fsecret.txt",
            "/img/%252e%252e/secret.txt", "/img/..%5csecret.txt",
            "/img//../../outside/private.png", "/img/../img-other/private.png",
            "/img/escape.png", "/img/escape-dir/private.png",
            "/img/logo.png%00", "/img/missing.png", "/img/nested/",
            "/etc/passwd", "/img/" + str(self.root / "secret.txt"),
        ]:
            with self.subTest(path=path):
                status, _, body = self.request(path)
                self.assertEqual(status, 404)
                self.assertEqual(body, b"Not found")

    def test_replacing_an_image_with_a_symlink_cannot_leak_files(self):
        image = self.root / "img" / "logo.png"
        image.unlink()
        image.symlink_to(self.root / "secret.txt")
        self.assertEqual(self.request("/img/logo.png")[2], b"PNG ASSET")
        self.handler = build.preview_handler()
        self.assertEqual(self.request("/img/logo.png")[0], 404)

    def test_symlinked_image_root_is_excluded(self):
        other = Path(self.temp.name) / "other-docs"
        other.mkdir()
        (other / "img").symlink_to(self.root / "img", target_is_directory=True)
        with patch.object(build, "ROOT", str(other)):
            self.assertEqual(build.preview_images(), {})

    def test_pages_reload_and_styles_still_work(self):
        status, _, body = self.request("/")
        self.assertEqual(status, 200)
        self.assertIn(b"Safe page", body)
        (self.root / "main.md").write_text("---\nslug: /\n---\n# Edited page\n")
        self.assertIn(b"Edited page", self.request("/index.html")[2])
        self.assertEqual(self.request("/style.css")[2], build.STYLE.encode())
        self.assertEqual(self.request("/missing.html")[0], 404)


if __name__ == "__main__":
    unittest.main()
