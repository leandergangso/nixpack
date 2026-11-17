# nixpack

`nixpack` bundles Nix derivations and their store paths into a portable Nix binary cache.

It resolves flake installables (or `.drv` files, store paths and `.nix` files), builds them locally, and copies the resulting closure into a self-contained cache directory or a single `.tar` archive. On the target machine the bundle is restored with `nix copy` — no install script needed.

## Features

* Packs the `.drv` graph and its source inputs
* Includes built outputs and their runtime closure by default
* Optional `-build-deps` includes the realized outputs of build-time dependencies for offline rebuilds
* Accepts flake installables, `.drv` paths, store paths, `.nix` files and spec files
* Output is a standard Nix binary cache (`nix copy --from`)
* Optional single-file `.tar` output for easy transfer
* Linux binaries for `amd64` and `arm64`

## Installation

Download the appropriate binary from the GitHub Releases page and place `nixpack` somewhere in your `PATH`.

Verify the installation:

```bash
nixpack --help
```

Nix (`nix`, `nix-store`) must be installed and in `PATH`. No other external tools are required.

## Usage

Bundle an installable into `./nixpack.tar`:

```bash
nixpack nixpkgs#hello
nixpack -file packages.txt
```

Write a cache directory instead of an archive:

```bash
nixpack .#myapp -out ./cache
nixpack /nix/store/...-hello.drv -out ./cache
nixpack ./default.nix -out ./cache
```

Where `packages.txt` contains one installable per line (`#` comments are ignored):

```text
nixpkgs#hello
nixpkgs#curl
/nix/store/...-local.drv
```

### Flags

```text
-out string        output .tar archive or directory (default "nixpack.tar")
-file string       file with installables (one per line)
-build-deps        include outputs of all build-time dependencies
-no-build          do not build installables locally (pack derivations only)
-version           print version and exit
```

## Dependency scope

By default `nixpack` includes:

* the top-level `.drv` files and their full input graph (nested `.drv` files and source paths)
* the built outputs of the requested installables and their runtime closure

Add `-build-deps` to also include outputs of every derivation in the recursive build graph. This makes it possible to rebuild on an offline machine without substituting or re-downloading compilers, libraries and other build inputs. The tradeoff is a significantly larger bundle.

Use `-no-build` to skip building locally and pack only derivations plus source paths — useful when the target machine does the building.

## Restoring on the offline machine

Extract the default archive and restore it with `nix copy`:

```bash
tar -xf nixpack.tar -C ./nixpack
nix copy --all --no-check-sigs --from file:///absolute/path/to/nixpack
```

If you packed with `-out ./cache`, skip the extraction and point the URL at that directory.

`--no-check-sigs` is required because locally created cache entries are unsigned. After the copy, the derivations are in the store and can be built or inspected, for example:

```bash
nix-build /nix/store/...-hello.drv
/nix/store/...-hello/bin/hello
```

## Notes

* This is a package-level tool, not a NixOS system closure packer.
* The source and target should use a compatible system/architecture for rebuilds.
* Paths already present in the local store are reused; no persistent cache is maintained by `nixpack` itself.

## License

MIT
