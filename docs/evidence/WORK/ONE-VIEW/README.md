# Design 15 evidence: one view, kept

| Step | Commit | Held by |
|---|---|---|
| One use-first rule | `73585e6` | `TestTheOneRule`, `TestPercentLeft` (internal/domain/usefirst); router and notification tests unchanged and passing |
| The server says what it means | `67f1fc4` | `TestTheViewSaysWhatItMeans`; demo server: 14 use-first, ¥112.29 + $16.50 held, page renders it, no console errors |
| The dataset is kept | this step | `TestSupplyCardsKeepTheLastRead` (store), `TestKeptCardsShowAtStart` (a restart shows the last read before reading; a key leaving the Mac takes its card), migration tests at v8 |

Panel, demo mode: relaunched with the server stopped, the first line Ant.app
printed was `kept: accounts=23 use-first=14` (drawn from
`~/Library/Caches/local.ant.app/view-demo.json`), before the server
answered. Logos are drawn from the app bundle (`panel-demo.png`).
