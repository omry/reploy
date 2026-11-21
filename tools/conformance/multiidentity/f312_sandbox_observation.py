#!/usr/bin/env python3
"""F3.1.2 effective-policy checks using the retained F3.1.1 startup fixture.

This currently qualifies the syscall/network child predicate only. Mount/root
and generation-bound admission remain separate mandatory F3.1.2 work.
"""
import importlib.util
from pathlib import Path

source = Path(__file__).with_name("f311_network_startup.py")
spec = importlib.util.spec_from_file_location("f312_startup_fixture", source)
fixture = importlib.util.module_from_spec(spec)
spec.loader.exec_module(fixture)
fixture.LABEL = "reploy.f312"
fixture.SCOPE = "F3.1.2.1 syscall/network component qualification; no mount admission or public activation"
fixture.EXTRA_SOURCES = (Path(__file__), fixture.ROOT / "internal/mappedlaunch/escape.go")
fixture.PAYLOAD = Path(__file__).with_name("f312_sandbox_observation_payload.go.txt")
raise SystemExit(fixture.main())
