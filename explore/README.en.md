# Research Assistant · Workspace space and information hierarchy

[简体中文](README.md) | English

> Status: historical design exploration record (2026-10). Current implementation follows [docs/design/2026-10-30-aperture-workbench.md](../docs/design/2026-10-30-aperture-workbench.md) and the current frontend code.

## User task

The interface put the conversation area and the DAG area on one horizontal page, making both feel cramped, while buttons and explanatory text were too dense. The goal was a cleaner, brighter interface with lower text density while keeping chat, execution graph, approvals, budget, literature library, and plan library.

## Three directions

| Direction | First-screen skeleton | Characteristics | Best for |
|---|---|---|---|
| A · Aperture · lightweight spatial workbench | 64px icon rail + 344px chat column + full-width execution canvas | White-green, rounded corners, soft grid; details in overlays; top bar keeps only status and budget | Keeping chat persistent while making the DAG the protagonist |
| B · Signal desk · dark task console | Top command bar + left session rail + center canvas + right node details + bottom chat dock | Dark graphite, orange run signals; approvals/logs/artifacts next to the current node | Long monitoring sessions with dense logs |
| C · Blueprint · split-stage research workbench | Full-width execution graph on top + full-width research notes/input below | Cool white and cobalt, blueprint grid, orthogonal connectors; split stages stop horizontal squeezing | Both chat and DAG need full width with minimal text |

## Recommendation

**C · Blueprint · split-stage research workbench** was recommended. It directly addresses the cramped shared horizontal page by splitting chat and DAG into two full-width stages of roughly 1400px each, leaving only status, budget, and to-do icons at the top. A suits long-lived chat; B suits monitoring-heavy work.

## Direction cards

### A · Aperture · lightweight spatial workbench

- North star: a bright, quiet research room where the execution graph is the only protagonist and chat is a readily available side panel.
- Generation engine: material × environment — frosted glass, pale green grid, soft natural light.
- Skeleton: left icon rail; chat column; execution canvas; minimal bottom status bar.
- Type: Aptos / Segoe UI Variable + Cascadia Mono; 27px headings, 13.5px body.
- Color: paper white #F7F9F4, forest green #1F6B4F, lime #D8F26A; drawn from lab glassware and botanical specimens.
- Key visual: glowing execution nodes with progress rings on active nodes.
- Control language: 14–18px radii, 1px strokes, low-contrast shadows; one solid green primary action.
- Rejected: horizontal toolbars, repeated stat rows, walls of small tags.

### B · Signal desk · dark task console

- North star: a mission-control console where current run state is readable at a glance and every action sits beside the current node.
- Generation engine: other-domain expression — aerospace/signal-console dark gauges and orange signals.
- Skeleton: top command bar; left session rail; center execution graph; right node details; bottom chat dock.
- Type: Bahnschrift + Cascadia Mono; 22px headings, 11.5–13.5px body.
- Color: deep space #070C10, graphite #0E1A21, signal orange #FF6B3D, mint #72DFB1.
- Key visual: orange active nodes with radar rings and flowing dashed lines.
- Control language: 6–11px small radii, thin strokes, glow only for running states.
- Rejected: large white areas, decorative illustrations, repeated explanatory text.

### C · Blueprint · split-stage research workbench

- North star: treat research as an engineering blueprint — structure above, judgment below; both get full width.
- Generation engine: a design movement — engineering drawing/blueprint grammar derived from the structure of research plans.
- Skeleton: thin top command bar; full-width execution graph above; full-width research notes + input below.
- Type: Georgia / Noto Serif SC headings + Segoe UI body + Cascadia Mono numbers; 17px headings, 12–13.5px body.
- Color: cool white #F3F6FD, pure white #FFFFFF, navy #0B1B3A, cobalt #1F4DFF.
- Key visual: cobalt active nodes and orthogonal connectors on a blueprint grid.
- Control language: 5–9px radii, 1px blue-grey strokes; cobalt primary, amber-only warnings.
- Rejected: left navigation, persistent detail sidebar, card grids.

## Evidence

- Direction first screens: `shots/explore-final3/a.png`, `b.png`, `c.png`, `sheet.png`
- Comparison page: `style-explorer.html`, screenshot `shots/explorer-final/page.png`
- Comparison cards: `shots/explorer-final/card-01/page.png`, `card-02/page.png`, `card-03/page.png`

## Verification notes

- All three directions were captured at 1440×900 with no console errors, horizontal overflow, or load failures.
- Comparison-page DOM check: all three cards were 445×278 first-screen thumbnails; a single view zoomed to 996×623 with typography, palette, and traits in the info panel.
- The session model did not support image input, so no human visual inspection was performed; DOM dimensions, contrast, overflow checks, and Oil UI screenshot lint were used instead.
- The `oil_ui_shoot` wrapper returned `ok:false` in that session; `shoot.mjs` from the same Oil UI installation was used directly for captures and checks.
