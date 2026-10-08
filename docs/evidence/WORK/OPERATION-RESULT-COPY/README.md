# Operation result copy repair

Date: 2026-10-05

## Change

The operation detail page used to render `OperationResult.code`, each reason's `group` and `code`, and `suggestedAction` directly. Those values are machine identifiers, not user instructions. The page now maps known result codes, reason pairs, and actions to Chinese and English explanations. Unknown values receive a plain-language fallback that directs the user to review the saved operation and usage before retrying.

The existing operation-detail layout and navigation are unchanged. The app test uses an unrecognized provider result/reason/action and verifies that all three raw identifiers stay out of rendered text while the explanatory fallback remains visible. A locale contract test now checks that Chinese and English dictionaries have the same keys and interpolation parameters.

## Verification

Executed from the repository root on 2026-10-05:

```text
make check test-contract test-integration test-web
exit 0
make check test-web
exit 0
```

The complete check, internal Go race suite, SQLite integration race suite, and Web suite passed. After adding the locale contract test, the final Web result was 7 test files and 244 tests passed; `make check` also passed again.

## Source hashes

```text
874c7f5a322d1b4baee7edc79cbcf567c5518c12e4726d6c3227a248ab910022  web/src/features/operations/OperationsPage.tsx
a8cc02ebb26aa5053a560d7240a32e10cc08ade2c9b83b357278c14c63026d3e  web/src/locales/zh.json
4e73a73747d9ddbe566bf1bbefef41654d8654671ecc744be517297996a2b5ce  web/src/locales/en.json
c44014448ec0c4fd43030540ff2f2720aaa2e5b6194efc92c06c3318e7dffb24  web/tests/app.test.tsx
6afbce19743fb3a4878930a9735557a106613a5b621cef28073e4f732fa340fb  web/tests/i18n.test.ts
```

This verifies the local operation-detail projection. It does not verify a live execution journey, browser screenshots at desktop/mobile breakpoints, or unseen provider reason codes beyond the generic fallback.
