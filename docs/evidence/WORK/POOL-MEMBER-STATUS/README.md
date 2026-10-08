# Resource pool member status correction

Date: 2026-10-05

The pool member badge now says “Enabled” / “已启用”, which describes the saved configuration without implying live capacity. Its enabled state requires the member, offer, binding, and owning account all to be enabled. The UI regression test disables the account while keeping the rest enabled and verifies that the member is shown as disabled.

Verification from the repository root:

```text
make check test-web
exit 0
```

The web suite passed with 7 files / 244 tests. The candidate planner mapping revision follow-up was then made and the complete check was rerun:

```text
make check test-contract test-integration test-web
exit 0
```

That run passed static checks, Go internal race tests, SQLite integration race tests, and all 244 Web tests.

Source hashes:

```text
889e91368e73bfa0970ae082a79efae6e4dc3dbf5dec44c7faf2f8984597c628  web/src/features/pools/PoolsPage.tsx
29f37c159c0443c4bf4373fccd53da1e522609f53208744b947e42f4b273aa5f  web/src/locales/zh.json
22d9f059fcfa6136ef9faf3af829638771095ef677294a7bc8f55d40b8932e0e  web/src/locales/en.json
3fbc818b64160b35e94e7f688c23c439dacb67fdd2a75d398f8cad98969683db  web/tests/app.test.tsx
```
