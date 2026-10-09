# Ant's logo

An ant in simple lines, no background: walking to the right, one antenna up, one eye. One
drawing everywhere (`ant.svg`, 32-unit grid, 2-unit round strokes), chosen by the user from the
drafts as "C2 · walking ant, line 2".

| File | Use |
|---|---|
| `ant.svg` | The mark, in `currentColor` |
| `favicon.svg` | The page's tab icon: dark lines, light lines in a dark browser theme |
| `app-icon.svg` | The app icon: the ant alone, no tile; a soft light outline under the lines keeps it visible on a dark Dock |
| `AppIcon.iconset/`, `Ant.icns` | Every macOS icon size, built from `app-icon.svg` |
| `menubar/AntTemplate.png`, `@2x` | The menu bar icon: a template image (black on transparent, 18 × 18 pt); macOS tints it for light and dark menu bars |
| `brand-sheet.png` | The app icon on light and dark Docks, the menu bar icon on light and dark bars |

Rebuild the PNGs and the `.icns` after changing an SVG: `sh scripts/brand.sh` (needs
`rsvg-convert`; `iconutil` comes with macOS). The page embeds the mark and the favicon inline
(it loads nothing from elsewhere).

There is no menu bar app yet; `menubar/` is ready for one (an `NSStatusItem` image named
`AntTemplate`).
