# ANT visual system review

Date: 2026-10-05

## Design direction

ANT is an AI resource-routing console. The interface uses a cool fog page ground, a deep marine navigation rail, a teal action color, and restrained amber for unresolved states. A small route-node mark anchors the identity; the rest of the interface uses restrained rules, clear type hierarchy, and no decorative gradients.

## Browser review

The Operations and Resources pages were rendered in the Codex in-app browser at 1440×1000 and 390×844. Responses came from disposable local mock APIs with synthetic operation, resource, and namespace values; no real credentials or provider calls were used.

- `desktop-1440.png`: complete table, desktop header, filter, and unknown-result status.
- `mobile-390.png`: compact two-row header and stacked operation fields.
- `resources-1440.png`: capacity, live balance, pool, and evidence columns at desktop width.
- `resources-390.png`: all ten resource fields shown as labeled rows on mobile.
- Mobile DOM metrics on both pages: viewport 390 px, document width 390 px, table container width 358 px, table scroll width 358 px, header height 108 px.
- Both mobile accessibility snapshots retained table headers and exposed each visual row label; browser console errors: none.

This checks the visual system and the Operations and Resources lists at these two widths. It is not a full seven-page T5 review or real-provider execution evidence.
