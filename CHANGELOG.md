# Changelog

All notable changes are listed here. Versions follow [Semantic Versioning](https://semver.org/).

## Unreleased

- `index`, `status` and `query`: capture of a worktree into the user cache, a snapshot with its files in a LadybugDB
  graph published atomically behind a workspace pointer, reader leases, read-only Cypher queries, YAML answers and
  error documents.
- LadybugDB v0.21.2 linked statically from the pinned release archive.
- Repository skeleton: README, MIT license, contributing guide, glossary, `make go` and CI.
- Design records: product boundary, collector declaration, errors and exit codes, snapshot storage and leases,
  dependencies as a source of types, parsing JavaScript, TypeScript and Vue with tree-sitter, building with LadybugDB.
