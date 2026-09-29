#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# End-to-end check: boots the backend in demo mode and exercises every API
# surface the frontend depends on. No cluster required.
set -euo pipefail

ADDR="${E2E_ADDR:-127.0.0.1:18082}"
BASE="http://${ADDR}"
BIN="${E2E_BIN:-/tmp/infracity-e2e}"

pass=0
fail=0
check() { # check <name> <curl-args...>
  local name="$1"; shift
  if out=$(curl -s -m 5 "$@" 2>&1); then
    pass=$((pass+1)); echo "ok   $name"
  else
    fail=$((fail+1)); echo "FAIL $name: $out"
  fi
}
jq_has() { # jq_has <name> <url> <python-expr-on-data>
  local name="$1" url="$2" expr="$3"
  if EXPR="$expr" URL="$url" python3 -c "
import json,os,urllib.request
d=json.load(urllib.request.urlopen(os.environ['URL'],timeout=5))
assert eval(os.environ['EXPR']), 'assertion failed on '+str(d)[:200]
" 2>/tmp/infracity-e2e-assert.log; then
    pass=$((pass+1)); echo "ok   $name"
  else
    fail=$((fail+1)); echo "FAIL $name"; tail -3 /tmp/infracity-e2e-assert.log
  fi
}

echo "==> building backend"
go build -o "$BIN" ./backend/cmd

echo "==> starting demo backend on $ADDR"
INFRACITY_DEMO=1 "$BIN" --addr="$ADDR" --snapshots-every=1s >/tmp/infracity-e2e.log 2>&1 &
pid=$!
trap 'kill $pid 2>/dev/null' EXIT
sleep 4

check health "$BASE/health"
check ready "$BASE/ready"
check prometheus-metrics "$BASE/metrics"
jq_has graph-nodes "$BASE/api/graph" "len(d['nodes'])>30"
jq_has graph-cluster-filter "$BASE/api/graph?cluster=production" "len(d['nodes'])>30"
jq_has graph-empty-cluster "$BASE/api/graph?cluster=nope" "len(d['nodes'])==0"
jq_has clusters "$BASE/api/clusters" "any(c['id']=='production' for c in d)"
jq_has namespaces "$BASE/api/namespaces?cluster=production" "len(d)>=3"
jq_has command-metrics "$BASE/api/metrics" "d['requestsPerSec']>1000 and d['pods']>0 and d['bytesPerSec']>0"
jq_has dependencies "$BASE/api/dependencies" "len(d)>5 and all(x['confidence']>0 for x in d)"
jq_has incidents-shape "$BASE/api/incidents" "isinstance(d,list)"
jq_has blast "$BASE/api/blast/service%2Fproduction%2Fpayments%2Fpayments" "len(d['downstream'])>=1"
jq_has risk "$BASE/api/risk/service%2Fproduction%2Fpayments%2Fpayments" "'score' in d and 'details' in d"
jq_has explain "$BASE/api/explain?target=service%2Fproduction%2Ffrontend%2Fcheckout" "'explanation' in d and len(d['explanation'])>20"
jq_has search "$BASE/api/search?q=payments" "len(d)>=2"
jq_has search-qualified "$BASE/api/search?q=svc%3Acheckout" "len(d)>=1"
jq_has self "$BASE/api/self" "d['graphNodes']>30 and 'wsConnections' in d and 'evictedNodes' in d and 'evictedEdges' in d"
sleep 2
jq_has snapshots "$BASE/api/snapshots" "len(d)>=1"

echo "==> ingest round-trip (open demo mode)"
code=$(curl -s -o /tmp/ing.json -w "%{http_code}" -m 5 -X POST "$BASE/api/v1/ingest" \
  -H 'Content-Type: application/json' \
  -d '{"clusterId":"e2e","nodeName":"n1","timestamp":1,"nodes":[{"id":"deployment/e2e/demo/web","type":"deployment","cluster":"e2e","namespace":"demo","name":"web"}],"edges":[],"stats":{}}')
if [ "$code" = "202" ]; then pass=$((pass+1)); echo "ok   ingest-202"; else fail=$((fail+1)); echo "FAIL ingest: $code"; fi
jq_has ingest-visible "$BASE/api/search?q=web" "any(n['id']=='deployment/e2e/demo/web' for n in d)"
jq_has changes-feed "$BASE/api/changes" "any('web' in c['summary'] for c in d)"
jq_has agent-status "$BASE/api/self" "any(a['clusterId']=='e2e' for a in d['agents'])"

echo "==> path explorer"
jq_has path "$BASE/api/path?from=external%2Fproduction%2Finternet&to=database%2Fproduction%2Fdata%2Fpostgres" "len(d['path'])>=4"

echo "==> ws route mounted (plain GET must NOT 200)"
code=$(curl -s -o /dev/null -w "%{http_code}" -m 5 "$BASE/ws/events")
if [ "$code" != "200" ]; then pass=$((pass+1)); echo "ok   ws-mounted ($code)"; else fail=$((fail+1)); echo "FAIL ws-mounted: plain GET returned 200"; fi

echo "----------------------------------------"
echo "pass=$pass fail=$fail"
[ "$fail" -eq 0 ]
