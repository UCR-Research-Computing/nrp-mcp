# Contributing

Thanks for helping. Bug reports, NRP policy corrections and small pull requests are all
welcome.

## Before you start

- For anything bigger than a small fix, open an issue first so we can agree on the shape.
- NRP policies change. If a rule in `internal/rules` or `internal/knowledge` is out of date,
  link the NRP documentation page in your issue or PR.

## Development

You need Go (see `go.mod`) and, for live tests, access to a Nautilus namespace.

```
bash scripts/gauntlet.sh      # gofmt, go vet, tests, build, ASCII-only docs (same as CI)
go build -o nrp-mcp ./cmd/nrp-mcp
```

Live tests create small, labelled CPU-only workloads and clean them up:

```
go run ./scripts/e2e  -bin ./nrp-mcp -ns <namespace>   # job, sweep, web plan
go run ./scripts/e2e2 -bin ./nrp-mcp -ns <namespace>   # volume, data, Jupyter session
```

They never create GPU pods or public endpoints. Run them only in a namespace you are
allowed to use.

## Conventions

- Every NRP rule has a test: a violating manifest is refused with the rule's id, and the
  fixed one passes.
- Tool results start with a plain-language `summary`; errors explain what to do.
- Nothing that creates, deletes or publishes runs without a confirm token.
- Docs are plain ASCII.
- Branch, pull request, green CI, squash merge.

## Code of conduct

This project follows the [Code of Conduct](CODE_OF_CONDUCT.md).
