# Ant's logo

An ant, in simple lines: walking to the right, one antenna up, one eye. Drawn on a 32-unit grid
with 2-unit round strokes (`ant.svg`), and redrawn for small sizes on an 18-unit grid
(`ant-small.svg`: no eye or knees, straight legs, heavier line), because the full drawing turns
to mush below about 24 px.

| File | Use |
|---|---|
| `ant.svg` | The mark, in `currentColor`: anywhere 24 px and up |
| `ant-small.svg` | The mark for 12–24 px (favicon, page header, menu bar) |
| `app-icon.svg`, `app-icon-small.svg` | The app icon: dark tile, cream ant, on the macOS 1024 grid (824 px tile, 185 px radius); the small drawing for the 16 and 32 px slots |
| `AppIcon.iconset/`, `Ant.icns` | Every macOS icon size, built from the two above |
| `menubar/AntTemplate.png`, `@2x` | The menu bar icon: a template image (black on transparent, 18 × 18 pt); macOS tints it for light and dark menu bars |
| `brand-sheet.png` | All of the above at a glance |

Colours: tile `#1f1e1c`–`#2b2a27`, lines `#f3efe6` (the page's own dark and light).

Rebuild every PNG and the `.icns` after changing an SVG: `sh scripts/brand.sh` (needs
`rsvg-convert`; `iconutil` comes with macOS). The page's header and favicon embed
`ant-small.svg`'s paths inline (the page loads nothing from elsewhere).

There is no menu bar app yet; `menubar/` is ready for it (an `NSStatusItem` image named
`AntTemplate`).
