#!/usr/bin/env bash
# Run on the same Linux host as the CLI; no business source modifications.
set -euo pipefail
cd "$(dirname "$0")/../.."
checkout=.localmock/repos/online-boutique
revision=38e7348eb289eb5b87c0c6e8cb19ced0449dc389
if [[ ! -d "$checkout/.git" ]]; then
  mkdir -p .localmock/repos
  git clone https://github.com/GoogleCloudPlatform/microservices-demo.git "$checkout"
  git -C "$checkout" checkout --detach "$revision"
fi
actual=$(git -C "$checkout" rev-parse HEAD)
if [[ "$actual" != "$revision" ]]; then
  echo "Expected upstream commit $revision; found $actual. Use a dedicated checkout at the expected revision." >&2
  exit 1
fi
if [[ -n "$(git -C "$checkout" status --porcelain --untracked-files=no)" ]]; then
  echo "Upstream tracked files were modified; restore a clean dedicated checkout first." >&2
  exit 1
fi
test -x bin/localmock
cp bin/localmock "$checkout/src/checkoutservice/localmock-health"
cp examples/online-boutique/Dockerfile.checkout "$checkout/src/checkoutservice/Dockerfile.localmock"
echo "Prepared checkout $revision"
