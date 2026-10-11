"""Include the Linux-only private harness tests in the existing CI test phase."""

import importlib
from pathlib import Path
import sys
import unittest


def load_tests(loader, tests, pattern):
    if not sys.platform.startswith("linux"):
        return tests
    directory = Path(__file__).resolve().parents[1] / "conformance/multiidentity"
    sys.path.insert(0, str(directory))
    try:
        return loader.loadTestsFromModule(importlib.import_module("test_conformance"))
    finally:
        sys.path.pop(0)
