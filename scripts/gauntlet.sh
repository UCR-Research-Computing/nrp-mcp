#!/usr/bin/env bash
# Local gauntlet: same checks as CI.
set -euo pipefail
cd "$(dirname "$0")/.."
test -z "$(gofmt -l .)" || { echo "gofmt:"; gofmt -l .; exit 1; }
go vet ./...
go test -count=1 ./...
go build -o /dev/null ./cmd/nrp-mcp
# ASCII-only docs (RC writing convention)
if LC_ALL=C grep -nP '[^\x00-\x7F]' README.md SPEC.md CHANGELOG.md SECURITY.md CONTRIBUTING.md CODE_OF_CONDUCT.md SUPPORT.md; then echo "non-ASCII in docs"; exit 1; fi
echo "gauntlet ok"
