# kartograph

[![CI](https://github.com/hakastein/kartograph/actions/workflows/ci.yml/badge.svg)](https://github.com/hakastein/kartograph/actions/workflows/ci.yml)

kartograph is a Go command-line tool for coding agents. It builds a graph of a repository's code, from frontend calls
through HTTP endpoints and PHP down to SQL tables, and answers questions about it: the path from a URL, component or
symbol to tables (`trace`), the endpoints, entry points and tables a change touches (`impact`), callers and callees,
the difference between two states of code (`diff`), where an edge comes from (`explain`) and how much of each link of
the chain is resolved (`coverage`).

**Status: in development.** The first commands capture a worktree and publish its snapshot (`index`), show the
published snapshot (`status`) and run a read-only Cypher query on it (`query`); the graph holds the files of the
snapshot.

## Building

kartograph links the LadybugDB static library, so a build needs Go, a C and C++ compiler, the OpenSSL 3 headers and
libraries, `curl` and `sha256sum`. Linux amd64 is the only pinned platform.

```bash
make build   # downloads the pinned LadybugDB library, checks its digest, builds bin/kartograph
```

The binary needs `libstdc++` and OpenSSL 3 at run time.

## Design in brief

- **A CLI only.** An agent calls kartograph from the shell. There is no MCP server, and no language server is used as a
  data source.
- **A graph is current or absent.** A query checks its snapshot first and updates it when the code changed. When the
  graph cannot be updated and built completely, the command fails instead of answering from a partial or stale graph.
  A name match never becomes an edge; a target that is not known statically is an explicit unresolved site.
- **Project knowledge lives in collectors.** The router, the frontend HTTP clients, the SQL layer and the entry points
  of a project are described by JavaScript collectors in the project's repository, run through
  [cliche](https://github.com/hakastein/cliche). The core knows no project concept.
- **One graph per worktree.** Each worktree, uncommitted changes included, gets its own immutable snapshot stored in
  LadybugDB under the user's cache. kartograph never writes to the source tree.
- **One YAML document per answer** on stdout; an error is one YAML document on stderr with `code` first.
- **Target platforms:** Linux and macOS, amd64 and arm64, with WSL counted as Linux. Builds for these platforms are
  planned and not yet verified; native Windows is not a target.

## Documentation

- Design records: [docs/adr/](docs/adr/) (in Russian).
  - [ADR-0001](docs/adr/0001-the-product-is-a-cli-over-a-current-graph.md): product boundary, module and license.
  - [ADR-0002](docs/adr/0002-a-collector-declares-itself-in-a-cliche-key.md): collector declaration.
  - [ADR-0003](docs/adr/0003-an-error-is-a-yaml-document-with-a-closed-code.md): errors and exit codes.
  - [ADR-0004](docs/adr/0004-a-snapshot-is-a-generation-behind-a-pointer.md): snapshot storage, publication and
    leases.
  - [ADR-0005](docs/adr/0005-dependencies-are-a-type-source.md): dependencies as a source of types.
  - [ADR-0006](docs/adr/0006-js-and-vue-are-parsed-with-tree-sitter.md): parsing JavaScript, TypeScript and Vue.
  - [ADR-0007](docs/adr/0007-ladybugdb-is-linked-statically.md): building with LadybugDB.
- Glossary: [CONTEXT.md](CONTEXT.md) (in Russian).
- Changes: [CHANGELOG.md](CHANGELOG.md).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

[MIT](LICENSE)
