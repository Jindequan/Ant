# 17. User-chosen supply route order

Ant's route preview ranks eligible offers for one canonical model. This design
lets a person reorder those choices without changing which offers qualify or
authorizing paid requests.

## Scope and rules

- The saved order is scoped by local namespace and canonical model ID. A model
  with no saved preference keeps Ant's deterministic default: class, score,
  freshness, then stable offer ID.
- A saved order ranks eligible offers. Paused, expired, unknown, or otherwise
  excluded offers remain excluded and are explained in `notUsed`. A stale
  account is marked with a freshness warning; staleness alone does not exclude
  an offer.
- The API accepts a complete permutation of the currently eligible offers.
  Invalid IDs, duplicates, missing IDs, a changed eligible set, or a stale
  revision fail without changing the saved order.
- An empty offer list is the explicit inverse operation: it clears the
  preference and returns the model to Ant's dynamic default ranking. The order
  revision still advances, so an older editor cannot resurrect the cleared
  preference.
- When an offer is temporarily ineligible, its ID stays dormant after the
  eligible part of the order. It has no effect while excluded; if the same
  stable offer returns, its preference returns. Newly eligible offers follow
  manually ranked offers and use Ant's deterministic default among themselves.
- A preference never implies permission to spend. The route preview remains
  read-only and every provider request still requires an explicit caller.

## Persistence and concurrent edits

Migration 009 adds `supply_route_orders`, keyed by `(namespace, model_id)`,
with a JSON offer-ID list and monotonic revision. A reorder supplies the
revision it saw. SQLite updates the preference only when that revision still
matches. Before writing a non-empty reorder, the engine re-runs the complete
qualification calculation against that same SQLite writer transaction and
compares its eligible offer set with the set the user saw. A change to the
eligible offer set before the transaction (for example, pausing a route or
crossing a relevant time boundary) causes HTTP 409 and no revision increment.
A reading or other state change that leaves the eligible offer set unchanged
does not by itself cause a conflict. An older editor also receives HTTP 409.
Preferences survive application restart and are independent across models.
Route previews return persisted `savedOfferIds` separately from the currently
eligible `use` list. Clients use the durable preference and revision to
reconcile an uncertain save even if eligibility changes afterward.

## Acceptance

Fake-provider API coverage uses Z.ai and DeepSeek for the same model. It checks
default order, explicit reorder, another model's independent version, restart,
simultaneous edits, pause exclusion, rejection of a paused offer, and restored
preference on resume, then reset to default. Deterministic SQLite interleavings
change pause, offers, readings, and time after the initial preview but before
the writer transaction; changes that alter the eligible set must conflict
without writing an order revision. A post-commit preview-read failure must
still report the saved order as committed. Store tests cover persisted
revisions and compare-and-swap conflicts.
The isolated demo Models UI also covers a failed first GET with retry, a reset
whose response is unknown but whose default preference is confirmed by GET
despite eligible routes remaining, and a manual save confirmed from
`savedOfferIds` after pausing one of its offers. A failed reconciliation GET
keeps pending state through a view rerender, and a newer revision is shown as
current without claiming the earlier save was confirmed.
