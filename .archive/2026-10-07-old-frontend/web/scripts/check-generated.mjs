#!/usr/bin/env node
/**
 * Generated-type drift gate (R05/R14): regenerate BOTH generated surfaces —
 * src/api/schema.d.ts (openapi-typescript) and src/api/config-document.gen.d.ts
 * (json-schema-to-typescript) — into a temporary directory and compare them
 * byte-for-byte against the committed files. Any drift exits non-zero, so a
 * contracts/ change without regeneration fails the build instead of silently
 * diverging from the hand-written re-exports in src/api/types.ts.
 */
import { spawnSync } from 'node:child_process';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const webRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const tmp = mkdtempSync(path.join(webRoot, 'node_modules/.guard-gen-'));
process.on('exit', () => rmSync(tmp, { recursive: true, force: true }));

const checks = [
  {
    name: 'schema.d.ts (openapi)',
    args: [
      path.join(webRoot, 'node_modules/.bin/openapi-typescript'),
      path.resolve(webRoot, '../contracts/openapi.yaml'),
      '-o',
      path.join(tmp, 'schema.d.ts'),
    ],
    committed: path.join(webRoot, 'src/api/schema.d.ts'),
  },
  {
    name: 'config-document.gen.d.ts (json schema)',
    args: [
      path.join(webRoot, 'node_modules/.bin/json2ts'),
      '-i',
      path.resolve(webRoot, '../contracts/schema/config-document.schema.json'),
      '-o',
      path.join(tmp, 'config-document.gen.d.ts'),
    ],
    committed: path.join(webRoot, 'src/api/config-document.gen.d.ts'),
  },
];

let failed = false;
for (const check of checks) {
  const run = spawnSync(check.args[0], check.args.slice(1), { cwd: webRoot, encoding: 'utf8' });
  if (run.status !== 0) {
    console.error(`guard:gen: regeneration failed for ${check.name}`);
    console.error(run.stdout ?? '');
    console.error(run.stderr ?? '');
    failed = true;
    continue;
  }
  const generated = readFileSync(check.args[check.args.indexOf('-o') + 1], 'utf8');
  const committedPath = path.relative(webRoot, check.committed);
  let committed;
  try {
    committed = readFileSync(check.committed, 'utf8');
  } catch {
    console.error(`guard:gen: ${committedPath} is missing — run \`npm run generate:types && npm run generate:config-types\``);
    failed = true;
    continue;
  }
  if (generated !== committed) {
    console.error(
      `guard:gen: DRIFT in ${committedPath} — the committed file does not match a fresh generation ` +
        `(generated ${generated.length} bytes vs committed ${committed.length} bytes). ` +
        'Run `npm run generate:types && npm run generate:config-types` and commit the result.',
    );
    failed = true;
  } else {
    console.log(`guard:gen: ${committedPath} up to date (${committed.length} bytes)`);
  }
}

process.exit(failed ? 1 : 0);
