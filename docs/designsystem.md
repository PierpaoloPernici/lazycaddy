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
- The Caddy section lists **one row per error diagnostic** (`E error · line N
  · path`), with the path relative to the root Caddyfile directory for
  imports (e.g. `snippets/auth.caddy`) and the full path when the diagnostic
  lives outside it. `Enter` selects the diagnostic's document and reveals its
  line / pinned token; `→` opens the full diagnostic detail; `←` from the
  detail returns to the review list.
- A failed `v` from the main view does not force the diagnostics modal open:
  it selects the first authoritative error's document and reveals its
  line / pinned token in the source pane (the `E` marker and red token show
  there), so the operator never has to hunt. The full list stays available
  in the `i` review; the diagnostics modal remains a detail surface for the
  review (`→`) and the delete/edit failure flows.
- Caddy is the only authority for syntax and validation; advisory findings are
  non-blocking and never replace `v`.
- `v` reuses the existing asynchronous validate workflow; the review restore is
  handled so that closing the Caddy diagnostics returns to the review it was
  opened from.
- The Caddy validation section shows `not run` before `v`, then the readable
  error count and summary, and is flagged `stale` when the document source
  changes after validation.
- The Caddy section states are still `not run` / `stale` / `clean`; when
  errors exist they are listed as per-diagnostic rows instead of an
  aggregate count.

## Authoritative caddy diagnostics overlay

After a failed `v` validation, the error diagnostics are **mapped onto the
source pane lines** of the document they belong to (no toggle, matching the
advisory lint):

- Error lines carry an `E` gutter marker, warning lines a `W`; the caddy
  marker outranks the advisory `!`/`i` on the same line.
- The offending token is styled in the caddy error style (bold red underline);
  when caddy reports no column the whole line is marked. Unreliable
  coordinates (no line, line beyond the source, column past the line end)
  never annotate the view.
- Diagnostics are matched to documents by clean path, so an import's errors
  appear only on that import's pane.
- The overlay shares the review's outcome: it disappears when the result is
  flagged `stale` after an edit or reload, and both surfaces never disagree.
- The source-title summary appends the count (for example ` · 2 caddy
  error(s)`).

## Source folding

Folding is a **display-only projection** of the tree expansion state: when a
structural tree row is collapsed, the block body becomes one indicator row.
Presentation rules:

- The folded view is driven by `caddyfile.FoldLayoutFor`, never by rewriting
the source: every byte, line number and source range stays valid for
patching, selection and copying. Quoted braces and heredocs are string
tokens and never create folds; comments, imports and leaf directives are
never foldable.
- A collapsed fold keeps the header and the closing brace visible; the
indicator row renders `⋯ N lines` in the fold-indicator style (dim, never
colour alone) at the exact gutter width, so source text never shifts
horizontally between folded and unfolded rows. The indicator row carries no
marker cell content and no source position.
- The tree's stable item key (document path + kind + name + exact range) is
the only source of truth for expansion, so fold state survives selection
changes, saves, reloads and rollbacks; a fold whose range no longer exists
simply stops matching. Source folding is the tree expansion state itself:
collapsing or expanding a structural tree row (`Enter`, `+`/`-`, `←`/`→`,
and the palette's expand/collapse all) folds or unfolds its source block,
and clicking an indicator row reopens the fold. There is no independent
fold command: the tree and the source pane can never disagree.
- A reveal (selection change, search hit, diagnostic or matcher jump, save
re-anchor) auto-expands every fold hiding the target line; the selected
node's own closing line is deliberately not a target, so selecting a folded
block keeps its fold closed. The pane title appends ` · N fold(s)` while
folds are active.
- Selection stays byte-exact across folds: mouse selection, `Shift`+arrows
and `y` copy the underlying source bytes (hidden lines included), the cursor
skips hidden lines and clamps to the nearest visible one, and a click on an
indicator row opens the fold instead of starting a selection.

## Screens, footers and command palette — common setting

Every screen has **one footer** and, when it makes sense, **one palette**
(`?`). The palette is always context-aware: it shows only the commands
that are actually available on the current screen, never the homepage
commands when the user is in Logs.

At 80 columns the footer must stay on one line, so it is navigation-only
and the operational keys live in the palette behind `?`.

### Simple screens — no palette, minimal footer

Transient confirmations, detail views and not-yet-interactive dashboards.
The footer shows only the actions that close or confirm the view; `?` is
not advertised and does not open the palette.

| Screen | Footer (80-col) | Palette (`?`) |
|---|---|---|
| `Unsaved confirm` | `s save · d discard & quit · Esc cancel` | — |
| `Change conflict` / `compare` | `r reload · Esc keep` / `↑/↓ scroll · Esc back` | — |
| `Save confirm` | `Enter save · Esc cancel` | — |
| `Reload confirm` | `Enter reload · Esc cancel` | — |
| `Rollback confirm` | `Enter rollback · Esc cancel` | — |
| `Structured add` (`a` / `n` / `o` / `m`) | `type directive · Enter plan & validate · Esc cancel` / `↑/↓ choose sibling · Enter move after & validate · Esc cancel` etc. | — |
| `Backups` (`B`) | `↑/↓ move · Enter/→ compare · Esc close` | — |
| `Diagnostics list` / `detail` | `↑/↓ navigate · Enter/+ or → detail · Esc/← close` / `↑/↓ scroll · Esc/← back` | — |
| `Diff` (`D`) | `↑/↓ scroll · PgUp/PgDown page · n/N hunk · h/l scroll · Esc close` (plus `Enter` verb when applicable) | — |
| `Search` (`/` / `Ctrl-F`) | `type to search · ↑/↓ move · Enter open · Esc close` | — |
| `Log detail` | `↑/↓ scroll · PgUp/PgDown page · Esc/← back` | — (palette reachable via `?` from the list, not from the detail) |
| `Log filter` (`F` in Logs) | `type filter · Enter apply · Esc cancel · Ctrl-U clear` | — |
| `Runtime dashboard` (`I`) | `↑/↓ move · PgUp/PgDown page · r refresh · y copy · Esc close` | — (palette hidden for now, display-only) |
| `TLS dashboard` (`T`) | `↑/↓ move · PgUp/PgDown page · r refresh · y copy · Esc close` | — (palette hidden for now, display-only) |

### Screens with command bar — footer + `?` palette, context-aware

Full-screen or highly interactive views. The footer is navigation-only
(`↑/↓`, `PgUp/PgDown`, primary `Enter` action, `Esc`, `?`) so it fits at
80 columns; every operational key (`f`/`F`/`c`/`p` in Logs, `v`/`s`/`D`
/`r`/`e`/`a`/`m`… on the homepage) is still a direct hotkey and is also
discoverable in the palette. The palette filters to the current screen:
`filteredCommands()` hides homepage `Source`/`Validation` commands when the
user is in Logs and hides `Logs`-only commands when the user is on the
homepage.

| Screen | Footer (80-col) | Palette (`?`) shows |
|---|---|---|
| **Homepage** (tree / source, no overlay) | `↑/↓ move · Enter toggle · PgUp/PgDown · +/- all · ? commands` (or `↑/↓ move · PgUp/PgDown · ? commands` when no branch) | `Navigation` (move, toggle, expand, matcher `g`, search `/`, help), `Validation` (`v` validate, `i` review, `D` diff, `s` save), `Source` (`e`/`E` edit, `a` add, `n` new, `o` reorder, `m` form, `d` delete, `y` copy), `Runtime & recovery` (`r` reload, `I` runtime, `T` TLS, `l` logs, `B` backups, `H` errors), `Application` (`q`, `?`) |
| **Logs list** (`l`) | `↑/↓ move · PgUp/PgDown · Enter detail · Esc close · ? commands` | `Navigation` (`move`), `Logs` (`f` follow, `F` filter, `c` clear, `p` pause, `Enter` detail), `Runtime & recovery` is **hidden** (its commands would appear disabled), `Source`/`Validation` hidden; plus global `y` copy, `help`, `q`, `?` |
| **Inline findings** (`i`) | `↑/↓ move · Enter reveal · → detail · v validate · Esc close` | `Navigation` + `Validation` (review) + global |
| **Error history** (`H`) | `↑/↓ scroll · PgUp/PgDown page · Esc close` + `?` when palette is enabled | `Navigation` + `Runtime & recovery` (`H`) + global |

When the palette is open the footer becomes `↑/↓ navigate · PgUp/PgDown scroll · Enter run · Esc close` and the underlying view is dimmed via `modalOverlay`.

### Rules for every footer at 80 columns

- One line, no wrapping. Long titles and long footers are truncated with `…` (via `truncateToWidth` / `ansi.Truncate`) so the two-pane height stays bounded and the header is never pushed off-screen (regression: `tmp/Caddyfile` at 80–86 columns lost the header and shifted the tree bottom border up by one).
- No `q quit` in any footer. Quit stays a direct hotkey (`q` / `Ctrl-C`) and a palette entry (`Application` → `Quit`), never a footer hint.
- The title never repeats a footer hint (`Esc close`, `move`, `validate` …).

## Checklist for new views

When adding a view, decide its class first, then apply:

- Full-screen: Title Case title, name + context only, no background, blank line
  below the title, command hints only in the footer.
- Modal: leave title style for the (pending) house review unless it is a new
  Commands/Search-style header that opts into ALL-CAPS.
- Always: one placement for command hints (the footer), colour is never the
  only signal, and rendering stays byte-lossless.
- **Footer must fit at 80 columns**: keep it navigation-only and put the rest
  behind `?`.
- **Palette must be context-aware**: `isCommandVisible()` hides homepage
  commands when the user is in Logs and vice-versa.
