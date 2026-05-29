#!/usr/bin/env bash
# Demonstrates that each alert rule actually fires.
#
# Usage:
#   ./scripts/demo_alerts.sh service-down     # stops flight-service to trigger ServiceDown
#   ./scripts/demo_alerts.sh high-error-rate  # spams 404s to trigger HighErrorRate
#   ./scripts/demo_alerts.sh status           # show current alerts in Prometheus + Alertmanager
#   ./scripts/demo_alerts.sh restore          # bring flight-service back up
#
# After triggering, observe firing alerts at:
#   http://localhost:9090/alerts        (Prometheus side — pending then firing)
#   http://localhost:9093               (Alertmanager UI — only firing)

set -euo pipefail
cd "$(dirname "$0")/.."

CASE="${1:-status}"

case "$CASE" in
  service-down)
    echo ">>> Stopping flight-service to trigger ServiceDown alert..."
    docker compose stop flight-service
    echo ">>> Wait ~75s (rule needs target down for 1m, plus a scrape cycle)."
    echo ">>> Then check: http://localhost:9090/alerts and http://localhost:9093"
    ;;

  high-error-rate)
    echo ">>> Sending 4xx traffic to push error_rate above 5% for 2m..."
    echo ">>> Hitting GET /bookings/<bogus-id> in a tight loop in the background."
    # 404s count as client_error in http_request_errors_total
    (
      for _ in $(seq 1 4000); do
        curl -s -o /dev/null "http://localhost:8080/bookings/00000000-0000-0000-0000-000000000000"
        sleep 0.05
      done
    ) &
    PID=$!
    echo ">>> Background load PID=${PID}. Let it run ~3m for the alert to go firing."
    echo ">>> Then check: http://localhost:9090/alerts and http://localhost:9093"
    echo ">>> To stop early:  kill ${PID}"
    ;;

  restore)
    echo ">>> Bringing flight-service back..."
    docker compose start flight-service
    ;;

  status)
    echo "=== Prometheus alerts ==="
    curl -s http://localhost:9090/api/v1/alerts \
      | python3 -c "
import json, sys
data = json.load(sys.stdin)['data']['alerts']
if not data:
    print('  (none)')
for a in data:
    print(f\"  {a['labels']['alertname']:<20} state={a['state']:<8} service={a['labels'].get('service','-')}\")
"
    echo
    echo "=== Alertmanager (firing only) ==="
    curl -s http://localhost:9093/api/v2/alerts \
      | python3 -c "
import json, sys
data = json.load(sys.stdin)
if not data:
    print('  (none)')
for a in data:
    name = a['labels']['alertname']
    state = a['status']['state']
    print(f\"  {name:<20} state={state}\")
"
    ;;

  *)
    echo "Unknown case: $CASE"
    echo "Usage: $0 [service-down | high-error-rate | restore | status]"
    exit 2
    ;;
esac
