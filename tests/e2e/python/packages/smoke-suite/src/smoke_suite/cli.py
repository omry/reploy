from __future__ import annotations

import argparse
import json
import ssl
import sys
import tempfile
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

# Public test-only TLS material for the HTTPS smoke server.
SMOKE_CERT = """-----BEGIN CERTIFICATE-----
MIIDCTCCAfGgAwIBAgIUcdYP4UkTEmC7bMV/TUdeoYngJCEwDQYJKoZIhvcNAQEL
BQAwFDESMBAGA1UEAwwJbG9jYWxob3N0MB4XDTI2MDcwMzE2NTQyM1oXDTM2MDYz
MDE2NTQyM1owFDESMBAGA1UEAwwJbG9jYWxob3N0MIIBIjANBgkqhkiG9w0BAQEF
AAOCAQ8AMIIBCgKCAQEAmSBYNL4ysi8irLPJNQwGsXS8B8W22BzuVNQOEzm+zVBp
j1iomL34TiUim9TsT+Gu5EICu2xOXvlrGyOwN/jWLfGLBPxgi5epxokW8oeYz9VE
G7J8bbGp4WlA4RTUeBSsFfA+H/FWuYEYvoaF7G4vk/fSUXWW5FhA1ZSKI6YlsDxc
3aPa7abKS2PySYXKH+CguFXCpS5hgW2vRFa8lKrMR54cLewrtQMOS/LUWKRlsxd3
No3xeP1ckhqRzYg1Kzfo/kc++a5MEx9qX2hQxbDa7qGCPLpuc4OTRohau/AZojeL
jcnySQw/p7AmbWRq8q81jT/cwIniezT13uJcaMbTwwIDAQABo1MwUTAdBgNVHQ4E
FgQUrTK41YpHvU6KH+HLLcQ9F8G7D40wHwYDVR0jBBgwFoAUrTK41YpHvU6KH+HL
LcQ9F8G7D40wDwYDVR0TAQH/BAUwAwEB/zANBgkqhkiG9w0BAQsFAAOCAQEAGBEg
0ZnWJp3aYocHDlEXA3Xv5GU00+RwHAFC0NDsYy13o8t7EgbC7KxImt23p4qUK5hq
SoC81lXxvbs4UopZkKffcjEit3xi5lU998po/itb6LBqHCg7ykUTNinJoSTacTgp
EVIdRFu7uBv6f2sed4QvU+CagLm0NyrtaBEtkDgU+4kD6vcZ+TSiIb9BetZirQTi
0VGZdQZpFOb7yn0rYgtWQboHp1KSi+3nTHlo+U5NbolCOMApwcDkMW/fK63wKjjO
Bg+yF7xE96+477Xt37wu1OulrMj6DKjZEqQYFZ1tOONIQa9N1KkBvGEh+/AIQscK
tuTUvYGDs7sLldkDAQ==
-----END CERTIFICATE-----
"""

SMOKE_KEY = """-----BEGIN PRIVATE KEY-----
MIIEvAIBADANBgkqhkiG9w0BAQEFAASCBKYwggSiAgEAAoIBAQCZIFg0vjKyLyKs
s8k1DAaxdLwHxbbYHO5U1A4TOb7NUGmPWKiYvfhOJSKb1OxP4a7kQgK7bE5e+Wsb
I7A3+NYt8YsE/GCLl6nGiRbyh5jP1UQbsnxtsanhaUDhFNR4FKwV8D4f8Va5gRi+
hoXsbi+T99JRdZbkWEDVlIojpiWwPFzdo9rtpspLY/JJhcof4KC4VcKlLmGBba9E
VryUqsxHnhwt7Cu1Aw5L8tRYpGWzF3c2jfF4/VySGpHNiDUrN+j+Rz75rkwTH2pf
aFDFsNruoYI8um5zg5NGiFq78BmiN4uNyfJJDD+nsCZtZGryrzWNP9zAieJ7NPXe
4lxoxtPDAgMBAAECggEARKcqxOLtidP2QOYOdPkiWxeSYg20D6rQ9Dlq4hXGxPij
i0BdvrWViTu/C4zoMlxl9DLgVLWXYZ+D9NQIYt/u7wRXpvGcO5nQ5ZF7N1uyGKRu
d9iLTwcTultdWkzjgb8K9H8U629hyaPmuy1HCBzSug2nPxxwfYpP7zI8yQTp6txD
cz8cOCVfyVs11pIEEVACWL5zXW9FmESmBZrbbgeb0xy/W/6EYoEEtSFgAjOcxMaC
7iajwqHlL7Pykv3q4keIr9H5lKcfBc5JjQVjeoU/SNEq0xKMeEe4d//aYTweLwmV
eARw6aMR0qjQ/LHeJZTlnjyMbMpMCCe2ZKrmZ46GwQKBgQDUOyP/cTo4zKXb+iUc
DHo7gFh31ICeLMDJs+h/74SX0UjeSUjQDOZvNcUvlP9zYbIb1DqD2TA065DGqZTD
9XMKMaDAS7eDI57eC+iVgaFws/ma+dtKP/eUOc/ERn4LaP4IRsdpkvfuClgjZryS
REDKvfXJykFcqlUnoWhd6+jk4wKBgQC4tL0TgVvia1k2Pk1pOgEmcD09v1ObB9ij
yE4Ymjb120M2IexN/soGX01AXWXBQR5VxrKoJw68UNc6XXDKVCVcnSTZWp9kdqFx
iouRRbWSuW/BGSlb0JsWq/ly+wn7GSNyww0EC4XxiQm92lPeKFqcpyP+nYgA8i83
KWZ8cwxroQKBgEE5L7X4aUk9c5eoR7FYEFSq/AVPtHtoC5Oxi7mNtbUmp3tREGSI
ImV5I/Gcm+ks1B0DWzxcChmpb6PuR/71NvaiC+ItIufVkaRyCnewEBpf5U0AjqlC
AWd3YOfUNjZxfOi0P+KtPS7V1QKEN13IRhVIzfnHA9Fjs6nrS/TZZIi7AoGASJvs
VBWfLFPs3xEA12PQ/e5TdMmYsTIVbNUaNjuxbVbDhi0xurt1aanfMXVFwgG6Thft
NYMdHNRet3fyFeecRFsWGEeyrwifkIXZNcOEjGhPUUZ15r0Lqo7yYcvj8YzBTaT1
oehxwDCR3stL+uI8NKbT1IzS4SRTgUjKHBQSBuECgYBXDTrOiMuEfMJT3Dm9ruKE
6SdVa/YtE27z6i25d2t9d1MPh47t7W2vr4ce039UgZrwFTt2iG7Ayp4PUleW8CBq
ahWRC2h6HvbD21YAzuSyCVQ5A3lEwPc76NRYqBeDkk48UK9HNB4hUNUiQGv/4zMn
o+vRcH1bqxA9+HeErwSl6Q==
-----END PRIVATE KEY-----
"""


def main() -> int:
    command = sys.argv[1:] or ["serve"]
    if command == ["config", "check"] or command == ["config", "check", "--live"]:
        print(json.dumps({"app": "smoke-suite", "check": "pass"}))
        return 0
    if command and command[0] == "serve":
        return serve(command[1:])
    print(f"unsupported smoke-suite command: {' '.join(command)}", file=sys.stderr)
    return 2


def serve(arguments: list[str]) -> int:
    parser = argparse.ArgumentParser(prog="smoke-server serve")
    parser.add_argument("--host", default="0.0.0.0")
    parser.add_argument("--port", type=int, default=8075)
    options = parser.parse_args(arguments)
    host = options.host
    port = options.port
    server = ThreadingHTTPServer((host, port), SmokeHandler)
    with tempfile.TemporaryDirectory(prefix="smoke-suite-tls-") as cert_dir:
        cert_path = Path(cert_dir) / "cert.pem"
        key_path = Path(cert_dir) / "key.pem"
        cert_path.write_text(SMOKE_CERT)
        key_path.write_text(SMOKE_KEY)
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        context.load_cert_chain(cert_path, key_path)
        server.socket = context.wrap_socket(server.socket, server_side=True)
        print(f"smoke-suite serving HTTPS on {host}:{port}", flush=True)
        server.serve_forever()
    return 0


class SmokeHandler(BaseHTTPRequestHandler):
    def do_GET(self) -> None:
        if self.path != "/_health_":
            self.send_response(404)
            self.end_headers()
            return
        body = b"ok\n"
        self.send_response(200)
        self.send_header("Content-Type", "text/plain")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, format: str, *args: object) -> None:
        return
