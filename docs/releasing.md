# Releasing lazycaddy

The first public release was `v0.1.0`. Releases are tag-driven and published
by GitHub Actions with GoReleaser. GitHub generates the release body from
merged pull requests using the categories in `.github/release.yml`; GoReleaser
publishes the binaries, archives and checksums.

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
- Every release starts with the header defined in `.goreleaser.yml`. It uses
  first-release wording for `v0.1.0`, a milestone introduction for `v0.2.0`
  and `v0.3.0`, a dedicated v0.4 milestone introduction for `v0.4.0`, and a
  dedicated patch-release introduction for `v0.4.1`, followed by GitHub's
  generated categorized changelog. Later versions use a reusable introduction.
- Windows and package-manager publishing are deferred until the release
  process and platform behavior are stable.

## Release-ready history

All pull requests and commits must already satisfy the release-ready change
contract in [AGENTS.md](../AGENTS.md) before tagging. Check the PR title,
Conventional Commit subjects, release disposition label and changelog decision
before merging. Release preparation must not rename or rewrite published
history; correct a release problem with a new patch version.

## Before tagging

Before starting a release, verify that the complete documentation set is
aligned with the actual merged implementation. Review `PLAN.md`, `VISION.md`,
`README.md`, `AGENTS.md`, `CONTRIBUTING.md`, `SECURITY.md`, every Markdown
guide under `docs/` (including this guide and the research record), the CLI
help text, the community-health files under `.github/`, and the GoReleaser and
GitHub release configuration. Confirm that completed roadmap items, safety
rules, supported flags, keybindings, release wording, contribution guidance,
security policy and documented limitations agree across those sources. Update
the relevant documentation in the release change before creating the tag; do
not defer documentation drift to a later release.

Run the complete local verification from a clean working tree:

```sh
make check
make release-check
make dist
```

`make dist` runs GoReleaser in snapshot mode and creates local archives for the
configured target matrix without creating a tag or publishing a release.
Install or copy an archive to a production-like machine to exercise the
artifact before publishing. Inspect the generated `dist/` archives and verify
that each archive contains the `lazycaddy` binary. Verify the checksums before
publishing:

```sh
cd dist
sha256sum -c checksums.txt
```

On macOS, use `shasum -a 256 -c checksums.txt` when `sha256sum` is not
available.

## Previewing release notes

Before tagging, verify that `.goreleaser.yml` contains the intended
version-specific introduction when the release needs one. The published body
must follow the established format: the GoReleaser introduction first, then
GitHub's categorized changelog and its `Full Changelog` link. Do not review the
GitHub API preview in isolation: it does not include the GoReleaser header.

Preview the exact GitHub-generated release body without creating a tag or a
release:

```sh
gh api --method POST \
  repos/PierpaoloPernici/lazycaddy/releases/generate-notes \
  -f tag_name=v0.4.1 \
  -f target_commitish=main \
  --jq '.body'
```

The preview uses merged pull requests and their labels. This release has a
previous tag, so the preview includes the changes since `v0.4.0`; review
the generated body before publishing. The API preview does not include
GoReleaser's configured release header; GoReleaser prepends it when creating
the release.

## Publishing

Release from an up-to-date `main` after reviewing the changelog and the
working tree:

```sh
git tag -a v0.4.1 -m "Release v0.4.1"
git push origin v0.4.1
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
