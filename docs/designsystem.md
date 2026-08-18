# Design System

This document is the canonical record of the UI/UX rules for lazycaddy's
terminal interface. It exists so rendering decisions are made consistently
instead of ad hoc: every new view, modal or pane follows the rules below, and
any intentional deviation must be recorded here first.

The rules were agreed during the Inline Findings review work (2026-08-18).
They complement the product-level guidance in `VISION.md` and `PLAN.md`.

## Foundational principles

- **One source of truth for hints**: commands and keybindings are shown once,
  in the system footer at the bottom of the screen. A view or pane title never
  repeats a command hint.
- **Colour is never the only signal**: any state that matters must also be
  conveyed by text or a distinct marker (for example `!` / `i` in a gutter),
  so the UI stays usable in monochrome terminals and for colour-blind users.
- **Byte-lossless rendering**: overlays and views never modify the underlying
  document source; they only add ANSI styling or non-destructive markers.

## View classes

There are two UI classes with different title rules: **full-screen views** and
**modals**.

A **full-screen view** replaces the tree/source panes while it is open (for
example Logs, Error history and Inline findings). A **modal** is layered on top
of the application chrome (for example Commands/Search, the save/reload/rollback
confirmations, the diagnostics dialog and the diff view).

The class of each view is decided by whether it replaces the panes in
`Model.View()`, not by its size.

## Title rules — full-screen views

1. **Title case, not ALL-CAPS.** The title uses Title Case (for example
   `Error history`, `Inline findings`, `Logs`). All-caps is reserved for the
   modal headers that explicitly opt into it (see below).
2. **Title carries the view name plus its context only.** A title may include a
   count or a path (for example `Error history · 5 entries`,
   `Inline findings (2)`, `Source · config/Caddyfile`), but never a command
   hint such as ` · Esc close`.
3. **No background on the title.** The title is rendered with `activeTitleStyle`
   (accent foreground) inside the pane border; it never uses an on-surface
   background fill.
4. **A blank line separates the title from the content** (`title + "\n\n" +
   content`), mirroring the Logs view.
5. **All command hints live in the system footer, once.**

## Title rules — modals

Modal headers are be evaluated together, not ad hoc. As of today:

- **Commands and Search keep their ALL-CAPS headers** (`COMMANDS`, `SEARCH`).
  These were an explicit choice and are not changed by the full-screen Title
  Case rule.
- Other modal titles (for example save/reload/rollback confirmations, the diff
  view and the diagnostic detail) are left unchanged pending a review of house
  style. Do not alter them to match the full-screen rule until that review is
  done.

## Commands in titles — general

The system footer is the single place that lists command hints (for example
`↑/↓ move · Enter reveal/details · v validate · Esc close`). A title must not
duplicate `close`, `move`, `validate` or any other command. The one thing we
retained is the explicit removal of ` · Esc close` from the Validation dialog
title; all other titles that still carry a command hint are modals pending the
review above.

## Advisory findings (Inline findings)

The advisory lint is **always visible** on the selected document's source pane;
there is no on/off toggle. Its presentation rules:

- Findings are overlaid in place with gutter markers (`!` for a hint, `i` for
  an info) **and** token styling, never colour alone.
- The source-title summary lives in the pane title (for example
  ` · 2 findings · [i] review`, or ` · advisory: clean`), never in the
  transient status strip.
- The review view splits content into two visually separated sections:
  `ADVISORY` (parse-tree findings) and `CADDY VALIDATION` (the authoritative
  caddy validate outcome). The two are never mixed.
- Caddy is the only authority for syntax and validation; advisory findings are
  non-blocking and never replace `v`.
- `v` reuses the existing asynchronous validate workflow; the review restore is
  handled so that closing the Caddy diagnostics returns to the review it was
  opened from.
- The Caddy validation section shows `not run` before `v`, then the readable
  error count and summary, and is flagged `stale` when the document source
  changes after validation.

## Checklist for new views

When adding a view, decide its class first, then apply:

- Full-screen: Title Case title, name + context only, no background, blank line
  below the title, command hints only in the footer.
- Modal: leave title style for the (pending) house review unless it is a new
  Commands/Search-style header that opts into ALL-CAPS.
- Always: one placement for command hints (the footer), colour is never the
  only signal, and rendering stays byte-lossless.
