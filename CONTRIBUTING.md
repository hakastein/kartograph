# Contributing

Bug reports and pull requests are welcome on [GitHub](https://github.com/hakastein/kartograph).

## Before you start

- The core knows no concept of a particular project. Conventions of a project (its router, frontend HTTP clients, SQL
  layer, entry points) belong in collectors that live in that project's repository, not here; see
  [ADR-0001](docs/adr/0001-the-product-is-a-cli-over-a-current-graph.md).
- An agent reaches kartograph through the CLI only. MCP servers and language servers as data sources are out of scope.
- Never add source code, file paths, data or dumps of a real project. Test fixtures are synthetic and public.
- Open an issue before a change of behaviour, of the output or error contract, or of the collector API. A change of a
  recorded decision updates its ADR in the same pull request.
- Design records ([`docs/adr/`](docs/adr/)) and the glossary ([`CONTEXT.md`](CONTEXT.md)) are in Russian.

## Development

```bash
make go   # gofmt check, go vet, go test -race
```

CI runs the same target on every push and pull request.

## Tests

- A test checks external behaviour: what the agent sees (stdout, stderr, exit code) and what is visible of snapshots
  (which snapshot is published, what is left after a failure, what is removed). Internals are not exported for tests.
- The seam is running kartograph on temporary workspace fixtures: real git trees, real collectors through cliche, real
  noverify, real LadybugDB files. Internal components are not replaced.
- Pure argument functions (path normalization, placeholder parsing, symbol keys) are covered by table tests without a
  process.
- Third-party libraries (noverify, LadybugDB, tree-sitter, cliche, cobra) are not tested, and prose is not compared.

## Pull requests

- One change per pull request, against `main`.
- Keep Go plain: the consumer declares an interface; no new concept for a hypothetical case.
- `make go` passes.

## Releases

A release is a `vX.Y.Z` tag on a commit of `main`, set by the maintainer after the merge, with an entry in
[CHANGELOG.md](CHANGELOG.md).
