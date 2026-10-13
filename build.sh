#!/usr/bin/env bash
set -e

IMAGE="mikrotik-panel"
TAG="armv7"
PLATFORM="linux/arm/v7"

echo "==> Building ${IMAGE}:${TAG} for ${PLATFORM}..."
docker buildx build --platform "$PLATFORM" -t "${IMAGE}:${TAG}" --load .

echo "==> Saving to panel.tar..."
docker save "${IMAGE}:${TAG}" -o panel.tar

SIZE=$(du -h panel.tar | cut -f1)
echo "==> Done! panel.tar ($SIZE)"