## Motivation

<!-- Why is this change needed? Link the issue or design discussion when one exists. -->

## Changes

<!-- Summarize the user-visible and internal changes. -->

## Safety and compatibility

- [ ] The Caddyfile remains the source of truth.
- [ ] No unrelated source bytes, imports or unsupported directives are rewritten.
- [ ] Writes, reloads and destructive actions remain explicit and guarded.
- [ ] Compatibility or migration impact is documented, or this change is internal-only.

## Verification

<!-- List the commands run and their results. -->

- [ ] `make check`
- [ ] `git diff --check`
- [ ] Additional checks: <!-- e.g. make test-race, make coverage -->

## Release disposition

<!-- Apply exactly one intentional label from .github/release.yml, or explain why skip-changelog is appropriate. -->

- Release label: <!-- breaking-change / enhancement / bug / dependencies / github_actions / documentation / skip-changelog -->
- Release-note or migration detail: <!-- write "None" when not applicable -->
