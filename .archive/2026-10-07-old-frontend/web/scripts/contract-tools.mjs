#!/usr/bin/env node
/**
 * Test-side contract tooling (not shipped in the app bundle):
 *
 * - loadOpenApiSchemas: parse contracts/openapi.yaml and produce one ajv-ready
 *   draft-2020-12 JSON Schema per components/schemas entry ($refs inlined).
 *   This mirrors the kin-openapi VisitJSON schema layer used by the Go
 *   contract tests on the same fixtures.
 * - canonicalQuantitySchema: the shared canonical quantity constraint
 *   ($defs/quantity of config-document.schema.json) as a standalone schema.
 * - parseJsonStrict: raw-text JSON decode with duplicate-key detection —
 *   the Web mirror of jsonx.DecodeStrict's token-level checks (JSON.parse
 *   cannot see duplicate keys; only strict decoding can, per the manifest's
 *   duplicate-key fixture).
 */
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { parse as parseYaml } from 'yaml';

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');

export function loadOpenApiDocument() {
  return parseYaml(readFileSync(path.join(repoRoot, 'contracts/openapi.yaml'), 'utf8'));
}

function inlineRef(node, schemas, stack) {
  if (Array.isArray(node)) return node.map((item) => inlineRef(item, schemas, stack));
  if (node === null || typeof node !== 'object') return node;
  if (typeof node['$ref'] === 'string') {
    const match = /^#\/components\/schemas\/(.+)$/.exec(node['$ref']);
    if (!match) throw new Error(`unsupported $ref ${node['$ref']}`);
    if (stack.has(match[1])) return node; // cycle guard (spec has none today)
    const target = schemas[match[1]];
    if (!target) throw new Error(`missing component schema ${match[1]}`);
    return inlineRef(target, schemas, new Set([...stack, match[1]]));
  }
  const out = {};
  for (const [key, value] of Object.entries(node)) out[key] = inlineRef(value, schemas, stack);
  return out;
}

/** name → ajv-ready JSON Schema for every components/schemas entry. */
export function loadOpenApiSchemas() {
  const spec = loadOpenApiDocument();
  const schemas = spec.components?.schemas;
  if (!schemas) throw new Error('openapi.yaml has no components/schemas');
  const out = {};
  for (const name of Object.keys(schemas)) {
    out[name] = inlineRef(schemas[name], schemas, new Set([name]));
  }
  return out;
}

/** The canonical quantity rule (config schema $defs/quantity) as a standalone schema. */
export function canonicalQuantitySchema() {
  const schema = JSON.parse(
    readFileSync(path.join(repoRoot, 'contracts/schema/config-document.schema.json'), 'utf8'),
  );
  return schema.$defs.quantity;
}

/**
 * Strict raw-text JSON decode (mirror of jsonx.DecodeStrict for the checks
 * the Web can perform at the token level): rejects duplicate object keys,
 * trailing data and non-JSON grammar (comments, NaN, single quotes).
 */
export function parseJsonStrict(text) {
  let i = 0;
  const fail = (msg) => {
    throw new SyntaxError(`${msg} at offset ${i}`);
  };
  const skipWs = () => {
    while (i < text.length && (text[i] === ' ' || text[i] === '\t' || text[i] === '\n' || text[i] === '\r')) i++;
  };
  const expect = (literal) => {
    if (text.startsWith(literal, i)) {
      i += literal.length;
    } else {
      fail(`invalid literal, expected ${literal}`);
    }
  };
  const parseHex4 = () => {
    let value = 0;
    for (let k = 0; k < 4; k++) {
      const c = text[i];
      const digit =
        c >= '0' && c <= '9' ? c.charCodeAt(0) - 48 :
        c >= 'a' && c <= 'f' ? c.charCodeAt(0) - 87 :
        c >= 'A' && c <= 'F' ? c.charCodeAt(0) - 55 : -1;
      if (digit < 0) fail('invalid \\u escape');
      value = value * 16 + digit;
      i++;
    }
    return value;
  };
  const parseString = () => {
    i++; // opening quote
    let out = '';
    for (;;) {
      if (i >= text.length) fail('unterminated string');
      const c = text[i];
      if (c === '"') {
        i++;
        return out;
      }
      if (c === '\\') {
        i++;
        const esc = text[i];
        switch (esc) {
          case '"': out += '"'; i++; break;
          case '\\': out += '\\'; i++; break;
          case '/': out += '/'; i++; break;
          case 'b': out += '\b'; i++; break;
          case 'f': out += '\f'; i++; break;
          case 'n': out += '\n'; i++; break;
          case 'r': out += '\r'; i++; break;
          case 't': out += '\t'; i++; break;
          case 'u': {
            i++;
            let code = parseHex4();
            if (code >= 0xd800 && code <= 0xdbff && text[i] === '\\' && text[i + 1] === 'u') {
              i += 2;
              const low = parseHex4();
              if (low >= 0xdc00 && low <= 0xdfff) {
                code = 0x10000 + ((code - 0xd800) << 10) + (low - 0xdc00);
              } else {
                fail('invalid surrogate pair');
              }
            }
            out += String.fromCodePoint(code);
            break;
          }
          default:
            fail(`invalid escape \\${esc ?? ''}`);
        }
      } else if (c.charCodeAt(0) < 0x20) {
        fail('raw control character in string');
      } else {
        out += c;
        i++;
      }
    }
  };
  const parseNumber = () => {
    const start = i;
    if (text[i] === '-') i++;
    if (text[i] === '0') {
      i++;
    } else if (text[i] >= '1' && text[i] <= '9') {
      while (text[i] >= '0' && text[i] <= '9') i++;
    } else {
      fail('invalid number');
    }
    if (text[i] === '.') {
      i++;
      if (!(text[i] >= '0' && text[i] <= '9')) fail('invalid fraction');
      while (text[i] >= '0' && text[i] <= '9') i++;
    }
    if (text[i] === 'e' || text[i] === 'E') {
      i++;
      if (text[i] === '+' || text[i] === '-') i++;
      if (!(text[i] >= '0' && text[i] <= '9')) fail('invalid exponent');
      while (text[i] >= '0' && text[i] <= '9') i++;
    }
    return Number(text.slice(start, i));
  };
  const parseValue = () => {
    skipWs();
    const c = text[i];
    if (c === '{') return parseObject();
    if (c === '[') return parseArray();
    if (c === '"') return parseString();
    if (text.startsWith('true', i)) { expect('true'); return true; }
    if (text.startsWith('false', i)) { expect('false'); return false; }
    if (text.startsWith('null', i)) { expect('null'); return null; }
    return parseNumber();
  };
  const parseObject = () => {
    i++; // {
    const out = {};
    const keys = new Set();
    skipWs();
    if (text[i] === '}') {
      i++;
      return out;
    }
    for (;;) {
      skipWs();
      if (text[i] !== '"') fail('expected object key');
      const key = parseString();
      if (keys.has(key)) fail(`duplicate object key "${key}"`);
      keys.add(key);
      skipWs();
      if (text[i] !== ':') fail('expected ":"');
      i++;
      out[key] = parseValue();
      skipWs();
      if (text[i] === ',') {
        i++;
        continue;
      }
      if (text[i] === '}') {
        i++;
        return out;
      }
      fail('expected "," or "}"');
    }
  };
  const parseArray = () => {
    i++; // [
    const out = [];
    skipWs();
    if (text[i] === ']') {
      i++;
      return out;
    }
    for (;;) {
      out.push(parseValue());
      skipWs();
      if (text[i] === ',') {
        i++;
        continue;
      }
      if (text[i] === ']') {
        i++;
        return out;
      }
      fail('expected "," or "]"');
    }
  };

  const value = parseValue();
  skipWs();
  if (i !== text.length) fail('trailing data');
  return value;
}

// Root keys of the ConfigDocument (schema "required" set): the web strict
// codec layer also rejects unknown root fields, mirroring Go strict decode
// into the typed struct (nested unknowns are covered by the schema layer's
// additionalProperties:false).
const KNOWN_ROOT_KEYS = new Set([
  'schemaVersion', 'accounts', 'bindings', 'resources', 'entitlementGroups',
  'offers', 'pools', 'grants', 'evidence', 'unsupportedDrafts',
]);

/** 'accept' | 'reject' of the strict config-document codec on raw text. */
export function strictDecodeConfigDocument(text) {
  let parsed;
  try {
    parsed = parseJsonStrict(text);
  } catch {
    return 'reject'; // duplicate keys, trailing data, bad grammar
  }
  if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) return 'reject';
  for (const key of Object.keys(parsed)) {
    if (!KNOWN_ROOT_KEYS.has(key)) return 'reject';
  }
  return 'accept';
}

/** Deterministic key-sorted JSON serialization (receipt-hash stand-in for EncodeCanonical). */
export function canonicalJson(value) {
  if (Array.isArray(value)) return `[${value.map(canonicalJson).join(',')}]`;
  if (value === null || typeof value !== 'object') return JSON.stringify(value);
  const keys = Object.keys(value).sort();
  return `{${keys.map((k) => `${JSON.stringify(k)}:${canonicalJson(value[k])}`).join(',')}}`;
}
