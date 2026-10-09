#!/usr/bin/env bash
set -euo pipefail

# Disposable credentials and container; no project .env or data volume is used.
ci_name="api2mcp-ci-$RANDOM"
ci_admin="ci-only-admin-token-never-use-in-production"
ci_key="$(openssl rand -base64 32)"
trap 'docker rm -f "$ci_name" >/dev/null 2>&1 || true' EXIT
docker run -d --name "$ci_name" \
  -p 127.0.0.1::8080 \
  -e ADMIN_TOKEN="$ci_admin" -e ENCRYPTION_KEY="$ci_key" \
  -e SEED_MOCK=false api2mcp:ci >/dev/null

ci_port="$(docker port "$ci_name" 8080/tcp | cut -d: -f2)"
export CI_ORIGIN="http://127.0.0.1:$ci_port" CI_ADMIN="$ci_admin"

python3 - <<'PY'
import http.cookiejar, json, os, time, urllib.request, urllib.error
origin = os.environ['CI_ORIGIN']
opener = urllib.request.build_opener(
    urllib.request.ProxyHandler({}),
    urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
for attempt in range(30):
    try:
        with opener.open(origin + '/healthz', timeout=2) as res:
            assert json.load(res)['status'] == 'ok'
        break
    except (OSError, urllib.error.URLError):
        time.sleep(1)
else:
    raise SystemExit('Container did not become healthy')
request = urllib.request.Request(origin + '/api/login',
    data=json.dumps({'token': os.environ['CI_ADMIN']}).encode(),
    headers={'Content-Type': 'application/json', 'Origin': origin})
with opener.open(request, timeout=5) as res:
    assert res.status == 200
with opener.open(origin + '/api/workspace', timeout=5) as res:
    workspace = json.load(res)
    assert workspace['documents'] == [] and workspace['servers'] == []
print('Container health, admin login and persistent store initialization passed.')
PY
