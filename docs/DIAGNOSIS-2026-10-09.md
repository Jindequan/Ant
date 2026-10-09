# Diagnosis: Ant on a real Mac (2026-10-09)

Ant.app was run by the user with their real accounts for the first time. Read-only inspection of
that instance (its own snapshot and stored account ids; nothing sent to any provider) and the
code behind each symptom.

## What Ant must give the user

| | Promise | Today |
|---|---|---|
| ✅ | **Truthful numbers** — what's left, exact, fresh | Holds (exact parsing, history, outlook) |
| 🔴 | **Accounts under control** — add, remove, sign in again, and the list obeys | Broken: removed accounts stay; dead and duplicate accounts accumulate |
| 🔴 | **Runs itself** — starts at login, reaches every provider, keeps sign-ins | Broken: the app has no proxy, no shell keys; fixed port 8080 |
| 🟠 | **At a glance** — the menu bar tells what to do now | Thin: one list in a plain menu |
| 🟠 | **Calm and clear** — one look, no clutter | Mostly; dead cards and an 82-row card break it |

## Findings (root causes, not symptoms)

| # | Sev | Symptom | Root cause | Fix |
|---|---|---|---|---|
| 1 | 🔴 | "Some accounts can't be deleted" (Bailian Pay-as-you-go, an old Token Plan) | An old combined Bailian record (mode `console`, still in `accounts.json`) was turned into its three instances **on every read**, never in the store. Deleting `aliyun-payg-signin` removed nothing that persists; the next read rebuilt it. (First suspected: a stale cache after deleting — disproved by a test; the delete does re-discover.) | Migrate once, in the store: the old record becomes today's records for good; two old records no longer make duplicates |
| 2 | 🔴 | A Bailian sign-in adds a Pay-as-you-go card that never works | "One sign-in connects every mode" also creates Pay-as-you-go, which the console token can't read ("add an AccessKey") | Sign-in connects only the modes that read with it |
| 3 | 🔴 | Old signed-out cards stay beside the new working ones | Signing in again from Add creates a new account; the dead one isn't retired | A new sign-in retires the provider's signed-out sign-ins of the same modes |
| 4 | 🟠 | Invisible leftovers in storage (`aliyun-console-signin`, `aliyun-browser`) | Records of a mode that no longer exists are kept forever | Dropped on start; their sealed values stay in the vault |
| 5 | 🔴 | Copilot: "TLS handshake timeout" | Ant.app starts without the login shell's environment: no `HTTPS_PROXY` (the user's shell sets one), no `PATH` tools, no `*_API_KEY`. Verified on the running server | The app imports the login shell's environment once and gives it to the server |
| 6 | 🔴 | Port 8080 | Fixed default, a port many tools use | Default 17890; the next free port when taken; the server writes where it listens (`~/.config/ant/server.json`) and the app reads it |
| 7 | 🟠 | Menu "too ugly", "only Use first" | A plain `NSMenu` with one section | A designed panel: header, Use first, Needs you, every account with its bar, actions |
| 8 | 🟡 | One card with 82 free-quota rows | Every grant listed | Grouped by what the model makes, soonest-ending first, the rest folded |
| 9 | 🟡 | Network failures read as provider errors | Raw Go error text | "Couldn't reach GitHub — check the proxy" |

## Order of work

1 → 2 → 3 → 4 (accounts obey) · 5 → 6 (runs itself) · 7 (menu panel) · 8 → 9 (clarity).
Each: a test that fails before the fix, gates green, one commit.
