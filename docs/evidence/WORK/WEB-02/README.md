# WEB-02 configuration workspace smoke

Date: 2026-10-04

This is a local end-to-end smoke in a disposable SQLite database with the
`localtest` catalogue. The administrator token was generated for this database
and discarded with the temporary instance; no real credential or provider was
used.

Observed browser path: sign in → create a configuration draft → save and run
server validation → confirm activation → read the new active revision. The
active document in this smoke intentionally contained empty collections; it
proves persistence and the state transitions, not a provider call or useful
production configuration.

The first browser attempt exposed a development-proxy Host rewrite that made
same-origin CSRF validation reject writes. The Vite proxy now preserves the
browser Host. The complete path then passed against the running Go server and
SQLite store.

Screenshots show the resulting configuration overview at desktop and 390px
mobile widths:

- `config-active-desktop.jpg`
- `config-active-mobile.jpg`

Automated evidence for this source tree: Web tests (236/236), TypeScript
typecheck, generated-type guard, `make check`, and production Vite build. The
configuration editor currently edits the versioned document as JSON; guided
provider, resource, credential, and pool forms remain future work.
