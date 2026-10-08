# Stage 1 — manage providers and resources smoothly

User direction (2026-10-08), verbatim: “stage 1: manage providers and resources smoothly. Although
there are too many different providers, products, resources in the world, you still should to make
sure you have reached this goal. make complex to simple, make chaotic to clear. everything should be
simple and clear.”

This file is the running record of what was checked against that bar and what changed.

## Pass 1 (2026-10-08): the page

**Found** (demo mode, desktop and 375 px):
1. The toolbar had ten controls in two rows: category tabs, search, three views, and four
   dropdowns. "Type" meant *media* in a dropdown and *payment kind* elsewhere; the Model dropdown
   repeated the search box and the Models view; "Group by" repeated the tabs.
2. Tab labels and section titles disagreed ("API balance"/"API balances", "Free"/"Free
   allowances").
3. Add account was a flat grid of 27 providers with no search and no sign of what was already
   connected; it grows with every provider.

**Changed** (`server/internal/webui/static`, −98/+46 lines):
- One toolbar row: category tabs · one search · sort · views. The search now also matches what an
  account makes (image, video…) and model ids, and narrows a card to the matching allowances, so
  the Type and Model dropdowns are gone. Cards are grouped by category, the same as the tabs; the
  Group dropdown is gone.
- One vocabulary: Subscriptions · API balances · Free allowances, in tabs, sections and the
  summary.
- Add account opens with a focused search (provider name or what it makes) and marks providers
  already connected.
- The browser opt-in (R6) is one sentence and one button inside the existing flow.

**Checked:** search for `image`, `video`, `glm-5.3`, `kimi-k3` and a miss; Add account search
`video` (6 providers) and `deep` (DeepSeek); no page overflow at 375 px (the tab strip scrolls);
four gates pass.

**Still to judge** (next passes): the Add flow's three steps (provider → way of paying → way of
reading) carry long paragraphs; the summary tile "Use before it's gone: 15" counts allowances
without saying so; card menus and the Timeline view were not reviewed yet.
