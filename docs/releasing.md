# Releasing lazycaddy

The first public release target is `v0.1.0`. Releases are tag-driven and
published by GitHub Actions with GoReleaser. GitHub generates the release body
from merged pull requests using the categories in `.github/release.yml`;
GoReleaser publishes the binaries, archives and checksums.

Local release checks require GoReleaser `2.17.1` or a compatible GoReleaser v2
release. Verify the installed version before starting:

```sh
goreleaser --version
```

## Release contract

- Published builds target Linux and macOS on `amd64` and `arm64`.
- Each release contains one `.tar.gz` archive per target and `checksums.txt`.
- The binary reports its version, commit and build date through `--version`.
- The default binary remains read-only; a release does not change the safety
  defaults of the application.
- Windows and package-manager publishing are deferred until the release
  process and platform behavior are stable.

## Before tagging

Run the complete local verification from a clean working tree:

```sh
make check
goreleaser check
goreleaser release --snapshot --clean
```

Inspect the generated `dist/` archives and verify that each archive contains
the `lazycaddy` binary. Verify the checksums before publishing:

```sh
cd dist
sha256sum -c checksums.txt
```

On macOS, use `shasum -a 256 -c checksums.txt` when `sha256sum` is not
available.

## Previewing release notes

Preview the exact GitHub-generated release body without creating a tag or a
release:

```sh
gh api --method POST \
  repos/PierpaoloPernici/lazycaddy/releases/generate-notes \
  -f tag_name=v0.1.0 \
  -f target_commitish=main \
  --jq '.body'
```

The preview uses merged pull requests and their labels. The first release has
no previous tag, so it may include the project's full history; review the
generated body before publishing.

## Publishing

Release from an up-to-date `main` after reviewing the changelog and the
working tree:

```sh
git tag -a v0.1.0 -m "Release v0.1.0"
git push origin v0.1.0
```

The `Release` workflow runs `make check`, builds the release matrix and
creates the GitHub Release with the archives and checksum file. Do not reuse
or move a published tag; fix a release problem in a new patch version.

## After publishing

Download one archive for each supported operating system, verify its checksum,
and run:

```sh
./lazycaddy --version
```

Confirm that the output contains the published version and that the TUI starts
in read-only mode. Record any release-specific compatibility or recovery note
in the GitHub Release description and update the README only when the public
installation instructions change.
