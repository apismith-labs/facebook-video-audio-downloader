"""Offline test support: fake Apify transport and a local media server.

Nothing here contacts the network. The local server exists so the download layer is exercised over
real HTTP (including ``Range``) without downloading a Facebook video.
"""
from __future__ import annotations

import email.message
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


class FakeTransport:
    """Records requests and returns queued responses/errors without network access."""

    def __init__(self, responses=()):
        self.responses = list(responses)
        self.requests = []

    def request(self, method: str, path: str, body=None, **kwargs):
        self.requests.append(
            {"method": method.upper(), "path": path, "body": body, "query": kwargs.get("query")}
        )
        if not self.responses:
            raise AssertionError("No synthetic transport response queued")
        response = self.responses.pop(0)
        if isinstance(response, Exception):
            raise response
        return response

    @property
    def post_count(self) -> int:
        return sum(r["method"] == "POST" for r in self.requests)

    @property
    def paths(self) -> list[str]:
        return [r["path"] for r in self.requests]


class FakeResponse:
    """Minimal context-managed response for the injectable media opener."""

    def __init__(self, body: bytes, *, status: int = 200, content_type: str = "video/mp4",
                 content_range: str | None = None, declared_length: int | None = None):
        self._body = memoryview(body)
        self._offset = 0
        self.status = status
        self.headers = email.message.Message()
        if content_type:
            self.headers["Content-Type"] = content_type
        self.headers["Content-Length"] = str(len(body) if declared_length is None else declared_length)
        if content_range:
            self.headers["Content-Range"] = content_range

    def read(self, size: int = -1) -> bytes:
        if size is None or size < 0:
            size = len(self._body) - self._offset
        chunk = bytes(self._body[self._offset:self._offset + size])
        self._offset += len(chunk)
        return chunk

    def __enter__(self):
        return self

    def __exit__(self, *_exc):
        return False


class _RangeHandler(BaseHTTPRequestHandler):
    payload = b""
    support_range = True

    def do_GET(self):  # noqa: N802 - BaseHTTPRequestHandler API
        payload = type(self).payload
        header = self.headers.get("Range") if type(self).support_range else None
        start = 0
        status = 200
        if header and header.startswith("bytes="):
            start = int(header.split("=", 1)[1].split("-", 1)[0] or 0)
            if start >= len(payload):
                self.send_response(416)
                self.send_header("Content-Range", f"bytes */{len(payload)}")
                self.send_header("Content-Length", "0")
                self.end_headers()
                return
            status = 206
        body = payload[start:]
        self.send_response(status)
        self.send_header("Content-Type", "video/mp4")
        self.send_header("Content-Length", str(len(body)))
        if status == 206:
            self.send_header("Content-Range", f"bytes {start}-{len(payload) - 1}/{len(payload)}")
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *_args):
        """Silence the default stderr logging."""


class LocalMediaServer:
    """Serves one payload on 127.0.0.1 with optional HTTP Range support."""

    def __init__(self, payload: bytes, *, support_range: bool = True):
        handler = type("Handler", (_RangeHandler,), {"payload": payload, "support_range": support_range})
        self._server = ThreadingHTTPServer(("127.0.0.1", 0), handler)
        self._thread = threading.Thread(target=self._server.serve_forever, daemon=True)

    def __enter__(self):
        self._thread.start()
        return self

    def __exit__(self, *_exc):
        self._server.shutdown()
        self._server.server_close()
        self._thread.join(timeout=5)

    @property
    def url(self) -> str:
        host, port = self._server.server_address[:2]
        return f"http://{host}:{port}/media.mp4"
