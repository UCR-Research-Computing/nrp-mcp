#!/usr/bin/env bash
# Local gauntlet: same checks as CI.
set -euo pipefail
cd "$(dirname "$0")/.."
test -z "$(gofmt -l .)" || { echo "gofmt:"; gofmt -l .; exit 1; }
go vet ./...
go test -count=1 -race ./...
go build -o /dev/null ./cmd/nrp-mcp
# ASCII-only docs (RC writing convention)
# perl, not grep -P: macOS/BSD grep has no -P and the check silently never ran there.
if perl -ne 'if (/[^\x00-\x7F]/) { print "$ARGV:$.: $_"; $bad = 1 } close ARGV if eof; END { exit($bad ? 0 : 1) }' README.md SPEC.md CHANGELOG.md SECURITY.md CONTRIBUTING.md CODE_OF_CONDUCT.md SUPPORT.md; then echo "non-ASCII in docs"; exit 1; fi
echo "gauntlet ok"
