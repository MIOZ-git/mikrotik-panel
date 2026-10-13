# build.ps1 — сборка образа MikroTik Panel
$ErrorActionPreference = "Stop"

$IMAGE = "mikrotik-panel"
$TAG = "armv7"
$PLATFORM = "linux/arm/v7"

Write-Host "==> Building ${IMAGE}:${TAG} for ${PLATFORM}..." -ForegroundColor Cyan

docker buildx build --platform $PLATFORM --tag "${IMAGE}:${TAG}" --load .

if ($LASTEXITCODE -ne 0) { throw "Build failed" }

Write-Host "==> Saving to panel.tar..." -ForegroundColor Cyan

docker save "${IMAGE}:${TAG}" -o panel.tar

if ($LASTEXITCODE -ne 0) { throw "Save failed" }

$size = [math]::Round((Get-Item panel.tar).Length / 1MB, 2)
Write-Host "==> Done! panel.tar ($size MB)" -ForegroundColor Green