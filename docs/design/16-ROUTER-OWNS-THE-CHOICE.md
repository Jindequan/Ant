# The router owns the choice

Version: V1.0 · 2026-10-10. The Models view must explain Ant's routing choice
without calculating a second choice in the browser.

## What the code showed

The Models view grouped account-reported model names, then ranked those
accounts in JavaScript from quota percentages, reset dates, and account kind.
The server already has `/api/route`, which applies the router's actual offers,
pauses, model scopes, free grants, and eligibility rules. The page only used
that preview after a row was opened; before opening it, the page displayed its
own, sometimes incorrect, “best account” and allowance.

## Decisions

1. The browser may group model names reported by accounts. Those names are
   catalog facts, not route decisions.
2. A model row shows the model, media type, and number of accounts that list
   it. It does not guess the best account, remaining quota, or route order.
3. Opening a row fetches `/api/route`; the server's ordered `use` and `notUsed`
   choices and reasons are the only route answer shown.
4. Accounts that report a model but are absent from the preview remain visible
   as an unranked list, so “reported by” is not confused with “routable”.
5. A model with no server route says so plainly. Previewing never sends a model
   request.
6. Each route choice carries its offer ID/version and the exact stored reading
   IDs/times used; the page shows a compact evidence summary for that choice.

## Acceptance

- No client-side allowance/expiry ranking remains in the Models view.
- The collapsed list makes no “best account” claim.
- Expanded rows render the server's route and distinguish account-reported
  models not included in that route.
- Eligible and excluded choices identify the stored offer and observation
  versions behind their explanation.
- The route preview remains read-only.
