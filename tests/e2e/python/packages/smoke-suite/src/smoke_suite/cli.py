from __future__ import annotations

import json
import sys


def main() -> int:
    command = sys.argv[1:] or ["serve"]
    if command == ["config", "check"] or command == ["config", "check", "--live"]:
        print(json.dumps({"app": "smoke-suite", "check": "pass"}))
        return 0
    if command == ["serve"]:
        print("smoke-suite serving")
        return 0
    print(f"unsupported smoke-suite command: {' '.join(command)}", file=sys.stderr)
    return 2
