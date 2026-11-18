#!/usr/bin/env bash
set -euo pipefail

scope="${1:?usage: start_pinned_docker.sh SCOPE}"
host_docker="${REDEPLOY_CI_HOST_DOCKER:-unix:///var/run/docker.sock}"
image="${REDEPLOY_CI_DOCKER_IMAGE:-docker:29.5.2-dind@sha256:6b9cd914eb9c6b342c040a49a27a5eb3804453bae6ecc90f7ff96133595a95e8}"
container_name="${REDEPLOY_CI_DOCKER_CONTAINER:-reploy-pinned-docker-${GITHUB_RUN_ID:-$$}}"

: "${GITHUB_ENV:?GITHUB_ENV is required}"
: "${GITHUB_PATH:?GITHUB_PATH is required}"

mkdir -p "$scope/tmp"
printf '%s\n' "$container_name" > "$scope/container-name"
printf '%s\n' '{"features":{"containerd-snapshotter":false},"storage-driver":"overlay2"}' > "$scope/daemon.json"

docker --host "$host_docker" rm --force --volumes "$container_name" >/dev/null 2>&1 || true
docker --host "$host_docker" run --detach --privileged --network host \
  --name "$container_name" \
  --env DOCKER_TLS_CERTDIR= \
  --mount "type=bind,source=$scope,target=$scope" \
  "$image" \
  dockerd --host "unix://$scope/docker.sock" --config-file "$scope/daemon.json"

for attempt in $(seq 1 60); do
  if sudo docker --host "unix://$scope/docker.sock" info > "$scope/info.txt" 2>&1; then
    sudo chown "$(id -u):$(id -g)" "$scope/docker.sock"
    mkdir -p "$scope/bin"
    docker --host "$host_docker" cp "$container_name:/usr/local/bin/docker" "$scope/bin/docker"
    chmod +x "$scope/bin/docker"
    printf 'DOCKER_HOST=unix://%s/docker.sock\n' "$scope" >> "$GITHUB_ENV"
    printf 'TMPDIR=%s/tmp\n' "$scope" >> "$GITHUB_ENV"
    printf 'REDEPLOY_CI_HOST_DOCKER=%s\n' "$host_docker" >> "$GITHUB_ENV"
    printf 'REDEPLOY_CI_DOCKER_CONTAINER=%s\n' "$container_name" >> "$GITHUB_ENV"
    printf 'REDEPLOY_CI_DOCKER_SCOPE=%s\n' "$scope" >> "$GITHUB_ENV"
    printf '%s\n' "$scope/bin" >> "$GITHUB_PATH"
    exit 0
  fi
  sleep 1
done

docker --host "$host_docker" logs "$container_name" || true
exit 1
