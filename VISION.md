# lazycaddy — Vision and Design Principles

## Vision

lazycaddy's vision is to be the most enjoyable terminal user interface for Caddy.

> The lazier way to manage your Caddyfile.

lazycaddy should make Caddy administration feel clear, fast and safe without hiding the configuration that users actually own.

It should help people understand what Caddy is doing, make carefully scoped changes, and verify the result without leaving the terminal.

The Caddyfile is not an implementation detail. It is the user's configuration, documentation and source of truth. lazycaddy should make working with it more pleasant while preserving the ability to use the normal Caddy CLI, an external editor, Git, or any other existing workflow.

## What lazycaddy should feel like

lazycaddy should be:

- familiar to users of LazyGit, LazyDocker, k9s and other excellent terminal tools;
- easy to understand without memorising a large keymap;
- fast enough to use during an incident;
- powerful enough for experienced Caddy administrators;
- conservative with user data and runtime state;
- honest about what it knows, what it changed and what it could not verify.

The application should feel like a capable terminal companion, not like a web dashboard squeezed into a terminal.

## Acknowledgements

lazycaddy is inspired by the ideas and craftsmanship behind:

- [LazyGit](https://github.com/jesseduffield/lazygit), especially its focus on discoverability, simplicity, safety, speed and a focused terminal workflow;
- [LazyDocker](https://github.com/jesseduffield/lazydocker), for showing how a complex operational system can become approachable through a practical terminal interface.

Thank you to the maintainers and contributors of both projects for creating tools that demonstrate how much can be accomplished with a thoughtful TUI. lazycaddy is an independent project with its own domain model, safety requirements and design decisions.

The structure of this document is also inspired by the public [LazyGit vision document](https://github.com/jesseduffield/lazygit/blob/master/VISION.md). The principles below are written specifically for Caddy and the Caddyfile.

## Design principles

### 1. Discoverability

Terminal interfaces should not require users to remember everything.

- Show the most relevant keybindings in the current context.
- Provide help without forcing the user to leave the current screen.
- Explain why an action is disabled.
- Make the selected server, file, site and runtime state obvious.
- Show the impact of an action through a diff, diagnostic, notification or status change.
- Make it easy to find sites, directives, files and available actions.
- Use labels and accessible text in addition to color and glyphs.

### 2. Simplicity

Caddy is powerful, but common tasks should remain simple.

- Make inspection, validation, diffing and reloading easy.
- Start in read-only mode.
- Use sensible defaults.
- Avoid exposing every Caddy option in the first screen.
- Prefer a small number of useful configuration options over endless customization.
- Keep advanced functionality available through raw source, the external editor and Caddy's own commands.

### 3. Safety

Configuration and runtime changes can take a service offline. Safety is the default.

- Never reload implicitly after an edit.
- Validate before writing or reloading.
- Show the diff before applying changes.
- Create a backup before replacing a source file.
- Require confirmation for reloads, deletes, stops and discarded changes.
- Detect concurrent changes and never overwrite them silently.
- Preserve a recoverable previous state after failures.
- Keep browsing available when write permissions, Caddy or the Admin API are unavailable.
- Do not require the whole application to run as root.

When speed and safety conflict, choose the safe default and make a faster path explicit.

### 4. Fidelity to Caddy

lazycaddy should work with Caddy, not against it.

- Use Caddy's formatter, validator, CLI and Admin API instead of reimplementing their behavior.
- Treat the Caddyfile as the source of truth.
- Preserve comments, whitespace, ordering, imports and source file boundaries.
- Preserve unknown directives and third-party plugin directives.
- Do not reconstruct the entire configuration from JSON.
- Make the smallest possible source-range edit.
- Keep raw Caddyfile editing available at all times.

The application may provide structured summaries and editors, but they must remain projections over the user's real source.

### 5. Power

Users should not need to leave lazycaddy for common advanced workflows.

- Expose useful information from Caddy's runtime and Admin API.
- Support the external editor through the user's existing editor choice.
- Provide raw source access for directives that do not have a structured view.
- Make logs, TLS information, imports, snippets and runtime state easy to inspect.
- Add structured editing incrementally, beginning with common directives.
- Keep an escape hatch for uncommon, plugin-specific or future Caddy features.

Power should come from composing existing Caddy capabilities, not from adding magic behavior.

### 6. Speed

The tool should be pleasant during both routine administration and urgent troubleshooting.

- Start quickly and show useful state as soon as it is available.
- Keep blocking work out of the interactive rendering path.
- Make common workflows possible with a small number of keypresses.
- Keep navigation responsive with large configurations and log streams.
- Prefer disabling an unavailable action with an explanation over hiding it unexpectedly.
- Preserve keyboard muscle memory across related screens.

### 7. Clarity of state

The user must be able to tell what is true at every point in a workflow.

Distinguish clearly between:

- the original bytes on disk;
- the current in-memory working copy;
- the last validated configuration;
- the saved configuration;
- the configuration loaded by Caddy;
- runtime state reported by the Admin API.

Never claim that a configuration is loaded, valid, reachable or applied unless lazycaddy can verify that state. Use an explicit unknown state when verification is not possible.

### 8. Least privilege and privacy

The application should reveal only the information needed for the current operation.

- Browsing should work without elevated privileges.
- Missing permissions should disable only the affected actions.
- Do not log secrets from environment variables, Caddyfiles or command output.
- Do not expose sensitive command output without a deliberate user action.
- Keep privilege boundaries narrow and explicit.
- Make the target server and configuration path visible before any write or reload.

### 9. Think about the codebase

Every feature has a maintenance cost.

- Keep the application modular and testable.
- Prefer small interfaces at integration boundaries.
- Avoid adding configuration options without a strong user need.
- Protect the lossless editing contract with fixtures and regression tests.
- Do not add a structured editor before the parser and patcher can preserve unsupported syntax.
- Keep compatibility with new Caddy versions as an explicit maintenance responsibility.

The best feature is not always the one with the most visible UI. Sometimes it is a smaller internal guarantee that prevents future data loss or complexity.

## Resolving conflicts

Some principles naturally compete:

- speed versus safety;
- simplicity versus power;
- structured convenience versus source fidelity;
- automation versus explicit control;
- new Caddy features versus a stable maintenance surface.

When principles conflict, use this order of preference:

1. Preserve user data and avoid unsafe runtime changes.
2. Keep the common workflow simple and discoverable.
3. Follow Caddy's documented behavior instead of inventing application-specific magic.
4. Preserve an escape hatch through raw source and external tools.
5. Add advanced behavior only when it can be tested and explained clearly.

This does not mean lazycaddy should be limited to the simplest possible use case. It means advanced power should be deliberate, visible and recoverable.

## Scope boundaries

lazycaddy is not:

- a web GUI;
- a replacement for the Caddy CLI;
- a configuration generator that takes ownership of the user's setup;
- a JSON-first configuration manager;
- a reason to hide the Caddyfile from experienced administrators.

Its purpose is to make the existing Caddy workflow safer, faster and more understandable from the terminal.

## Relationship with the project plan

This document describes why lazycaddy exists and how it should feel. PLAN.md translates the vision into scope, architecture, safety rules, compatibility work and delivery milestones. AGENTS.md defines the working rules for contributors and AI agents.

When a proposed feature conflicts with this vision, revisit the product decision before implementing it.
