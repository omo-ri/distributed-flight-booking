#!/usr/bin/env python3
"""Verify SLO thresholds against Prometheus after a load run.

Queries the Prometheus HTTP API for the metrics defined in README's
SLI/SLO section and exits non-zero (so the CI step fails) when any
threshold is breached.

Thresholds (must match the SLI/SLO table in README):
  - booking-service error rate over the last 5m  <  1%   (SLO);  fails at >5% (failure threshold)
  - booking-service p95 latency over the last 5m  <  500ms (SLO); fails at >1000ms

The script always writes the raw measurements + verdict to
`metrics-report.json` so the CI job can attach it as an artifact.
"""

from __future__ import annotations

import json
import os
import sys
import time
from urllib.parse import urlencode
from urllib.request import urlopen


PROM_URL = os.environ.get("PROM_URL", "http://localhost:9090")

# Thresholds — keep in sync with README "SLI/SLO" section.
ERROR_RATE_SLO = 0.01          # 1%
LATENCY_P95_SLO_SECONDS = 0.5  # 500ms

ERROR_RATE_FAIL = 0.05         # 5%  — failure threshold for the alert/SLO
LATENCY_P95_FAIL_SECONDS = 1.0 # 1s


def prom_query(expr: str) -> float | None:
    """Run an instant PromQL query; return the first sample's value or None."""
    qs = urlencode({"query": expr})
    with urlopen(f"{PROM_URL}/api/v1/query?{qs}", timeout=10) as resp:
        data = json.load(resp)
    if data.get("status") != "success":
        raise RuntimeError(f"prom query failed: {data}")
    result = data["data"]["result"]
    if not result:
        return None
    return float(result[0]["value"][1])


def main() -> int:
    # Give Prometheus 2 scrape cycles to ingest the tail of the load run.
    time.sleep(15)

    # SLI 1: API availability — derived from error rate over 5m window.
    error_rate_expr = (
        'sum(rate(http_request_errors_total{service="booking-service"}[5m])) '
        '/ clamp_min(sum(rate(http_requests_total{service="booking-service"}[5m])), 1e-9)'
    )
    # SLI 2: API latency — p95 over 5m window.
    p95_expr = (
        'histogram_quantile(0.95, '
        'sum by (le) (rate(http_request_duration_seconds_bucket{service="booking-service"}[5m])))'
    )

    error_rate = prom_query(error_rate_expr) or 0.0
    p95 = prom_query(p95_expr) or 0.0

    report = {
        "prometheus_url": PROM_URL,
        "queried_at": int(time.time()),
        "measurements": {
            "error_rate":              error_rate,
            "latency_p95_seconds":     p95,
        },
        "slo": {
            "error_rate":              ERROR_RATE_SLO,
            "latency_p95_seconds":     LATENCY_P95_SLO_SECONDS,
        },
        "failure_threshold": {
            "error_rate":              ERROR_RATE_FAIL,
            "latency_p95_seconds":     LATENCY_P95_FAIL_SECONDS,
        },
    }

    verdicts: dict[str, str] = {}
    failed = False

    if error_rate >= ERROR_RATE_FAIL:
        verdicts["error_rate"] = f"FAIL ({error_rate:.4%} ≥ {ERROR_RATE_FAIL:.0%})"
        failed = True
    elif error_rate >= ERROR_RATE_SLO:
        verdicts["error_rate"] = f"DEGRADED ({error_rate:.4%} ≥ SLO {ERROR_RATE_SLO:.0%})"
    else:
        verdicts["error_rate"] = f"OK ({error_rate:.4%} < SLO {ERROR_RATE_SLO:.0%})"

    if p95 >= LATENCY_P95_FAIL_SECONDS:
        verdicts["latency_p95_seconds"] = f"FAIL ({p95*1000:.1f}ms ≥ {LATENCY_P95_FAIL_SECONDS*1000:.0f}ms)"
        failed = True
    elif p95 >= LATENCY_P95_SLO_SECONDS:
        verdicts["latency_p95_seconds"] = f"DEGRADED ({p95*1000:.1f}ms ≥ SLO {LATENCY_P95_SLO_SECONDS*1000:.0f}ms)"
    else:
        verdicts["latency_p95_seconds"] = f"OK ({p95*1000:.1f}ms < SLO {LATENCY_P95_SLO_SECONDS*1000:.0f}ms)"

    report["verdicts"] = verdicts
    report["status"] = "FAIL" if failed else "PASS"

    with open("metrics-report.json", "w") as f:
        json.dump(report, f, indent=2)

    print("=== Prometheus SLO verification ===")
    for k, v in verdicts.items():
        print(f"  {k:<25} {v}")
    print(f"  overall                   {report['status']}")

    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
