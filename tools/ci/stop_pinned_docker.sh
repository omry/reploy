#!/usr/bin/env bash
set -euo pipefail

scope="${REDEPLOY_CI_DOCKER_SCOPE:-${1:-}}"
host_docker="${REDEPLOY_CI_HOST_DOCKER:-unix:///var/run/docker.sock}"
container_name="${REDEPLOY_CI_DOCKER_CONTAINER:-}"
if [[ -z "$container_name" && -n "$scope" && -f "$scope/container-name" ]]; then
  container_name="$(cat "$scope/container-name")"
fi

if [[ -z "$container_name" ]]; then
  exit 0
fi

if [[ -n "$scope" ]]; then
  docker --host "$host_docker" logs "$container_name" > "$scope/daemon.log" 2>&1 || true
  docker --host "$host_docker" inspect "$container_name" > "$scope/inspect.json" 2>&1 || true
fi
docker --host "$host_docker" rm --force --volumes "$container_name" >/dev/null 2>&1 || true
