# Deploy the engine-power feature into the running WeKnora containers.
# Usage (after Docker Desktop is up): powershell -ExecutionPolicy Bypass -File scripts\deploy_engine_power.ps1
# - Copies the freshly built backend binary into WeKnora-app and restarts it
# - Copies the freshly built frontend dist into WeKnora-frontend
# - Verifies the new API route returns 401 (route exists, auth enforced)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
Set-Location $root

function Fail($msg) { Write-Host "FAILED: $msg" -ForegroundColor Red; exit 1 }

docker version --format '{{.Server.Version}}' *> $null
if ($LASTEXITCODE -ne 0) { Fail 'Docker engine is not running. Start Docker Desktop first.' }

# 1) backend binary
$binary = Join-Path $root 'WeKnora'
if (-not (Test-Path $binary)) { Fail "backend binary not found: $binary (run scripts/build-anydoc-image.sh --binary-only first)" }

$targets = @('/app/WeKnora', '/WeKnora', '/app/weknora', '/weknora')
$target = $null
foreach ($t in $targets) {
    docker exec WeKnora-app test -f $t 2>$null
    if ($LASTEXITCODE -eq 0) { $target = $t; break }
}
if (-not $target) { Fail 'cannot locate the backend binary inside WeKnora-app; inspect the image entrypoint manually.' }

Write-Host "copying backend binary -> WeKnora-app:$target"
docker cp $binary "WeKnora-app:$target"
docker restart WeKnora-app | Out-Null

# 2) frontend dist
$dist = Join-Path $root 'frontend\dist'
if (-not (Test-Path $dist)) { Fail "frontend dist not found: $dist" }
Write-Host 'copying frontend dist -> WeKnora-frontend:/usr/share/nginx/html'
docker cp "$dist\." 'WeKnora-frontend:/usr/share/nginx/html/'
docker exec WeKnora-frontend nginx -s reload 2>$null | Out-Null

# 3) verify route (401 means the route exists and auth is required)
Start-Sleep -Seconds 8
$code = (curl.exe -s -o NUL -w "%{http_code}" http://127.0.0.1:18080/api/v1/system/parser-engines/power)
Write-Host "parser-engines/power http_code=$code (expect 401)"
if ($code -ne '401') { Write-Host 'WARNING: unexpected status; check backend logs: docker logs --tail 100 WeKnora-app' -ForegroundColor Yellow }
Write-Host 'Done.'
