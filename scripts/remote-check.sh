#!/usr/bin/env bash
# Development transport only; the product itself is a local CLI.
set -euo pipefail
target=${1:?Usage: remote-check.sh user@debian-host}
remote_dir=${LOCALMOCK_REMOTE_DIR:-local-mock}
go_proxy=${LOCALMOCK_GOPROXY:-https://proxy.golang.org,direct}
if [[ ! "$remote_dir" =~ ^[A-Za-z0-9_./-]+$ || "$remote_dir" == /* || "$remote_dir" == *..* ]]; then
  echo "LOCALMOCK_REMOTE_DIR must be a simple relative directory" >&2
  exit 2
fi
if [[ ! "$go_proxy" =~ ^[A-Za-z0-9:/.,_-]+$ ]]; then
  echo "LOCALMOCK_GOPROXY contains unsupported characters" >&2
  exit 2
fi
ssh_args=()
if [[ -n "${LOCALMOCK_SSH_CONTROL:-}" ]]; then ssh_args+=(-S "$LOCALMOCK_SSH_CONTROL"); fi
ssh "${ssh_args[@]}" "$target" "mkdir -p \"\$HOME/$remote_dir\" \"\$HOME/.cache/localmock-go\""
tar_args=(--exclude=.git --exclude=.idea --exclude=bin --exclude=.localmock --exclude=.env --exclude='.env.*' --exclude='._*')
if [[ "$(uname -s)" == Darwin ]]; then tar_args+=(--no-xattrs); fi
COPYFILE_DISABLE=1 tar "${tar_args[@]}" -czf - . |
  ssh "${ssh_args[@]}" "$target" "tar -xzf - -C \"\$HOME/$remote_dir\""
ssh "${ssh_args[@]}" "$target" "cd \"\$HOME/$remote_dir\" && docker run --rm \
  --user \"\$(id -u):\$(id -g)\" \
  -v \"\$HOME/$remote_dir:/workspace\" \
  -v \"\$HOME/.cache/localmock-go:/cache\" \
  -w /workspace -e GOPATH=/cache/go -e GOCACHE=/cache/build -e GOPROXY='$go_proxy' \
  golang:1.25-bookworm sh -c \
  'go mod download && go test -race ./... && go vet ./... && CGO_ENABLED=0 go build -trimpath -o bin/localmock ./cmd/localmock && go test -c -o bin/lifecycle.test ./tests/integration'"
ssh "${ssh_args[@]}" "$target" "cd \"\$HOME/$remote_dir\" && LOCALMOCK_DOCKER_TEST=1 ./bin/lifecycle.test -test.v"
