# Release

Release artifacts are built from the Go CLI and written under ignored
`generated/release/`.

Build a local snapshot:

```bash
make release-snapshot
```

Override version metadata:

```bash
make release-snapshot VERSION=0.1.0
```

The snapshot target builds `pgworkbench` archives for common Linux and macOS
platforms. Default CI keeps using source-based checks; tagged release publishing
can be added once the MVP contract is stable.
