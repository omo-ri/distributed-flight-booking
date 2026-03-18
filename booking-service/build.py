#!/usr/bin/env python3
"""Generate Go code from OpenAPI spec using oapi-codegen."""

import os
import subprocess
import sys

BOOKING_SERVICE_DIR = os.path.dirname(os.path.abspath(__file__))
API_DIR = os.path.join(BOOKING_SERVICE_DIR, "api")
OUTPUT_FILE = os.path.join(API_DIR, "api.gen.go")

OPENAPI_SPEC = os.path.join(API_DIR, "openapi.yaml")
CONFIG = os.path.join(API_DIR, "oapi-codegen.yaml")


def main():
    cmd = [
        "oapi-codegen",
        "--config", CONFIG,
        "-o", OUTPUT_FILE,
        OPENAPI_SPEC,
    ]

    print(f"Running: {' '.join(cmd)}")
    result = subprocess.run(cmd, cwd=BOOKING_SERVICE_DIR)
    if result.returncode != 0:
        print("oapi-codegen failed", file=sys.stderr)
        sys.exit(1)

    print(f"Generated: {OUTPUT_FILE}")


if __name__ == "__main__":
    main()