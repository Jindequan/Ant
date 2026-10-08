#!/usr/bin/env node
/**
 * Regenerate the ConfigDocument TypeScript types from the frozen JSON Schema
 * (contracts/schema/config-document.schema.json). Output is byte-deterministic
 * for a given schema + json-schema-to-typescript version; `npm run guard:gen`
 * fails if the committed file drifted.
 *
 * R05: the generated file is the single consumption path for the
 * ConfigDocument family (re-exported by src/api/types.ts).
 */
import { compileFromFile } from 'json-schema-to-typescript';
import { mkdirSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const webRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const schemaPath = path.resolve(webRoot, '../contracts/schema/config-document.schema.json');
const outPath = path.join(webRoot, 'src/api/config-document.gen.d.ts');

mkdirSync(path.dirname(outPath), { recursive: true });
const output = await compileFromFile(schemaPath);
writeFileSync(outPath, output);
console.log(`config types → ${path.relative(webRoot, outPath)} (${output.length} bytes)`);
