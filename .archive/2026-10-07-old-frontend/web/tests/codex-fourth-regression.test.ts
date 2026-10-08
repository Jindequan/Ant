import {readFileSync} from 'node:fs';
import {describe,it,expect} from 'vitest';
import {validateConfigDocument,createDescriptorCatalog} from '../src/api/validate';
import descriptor from '../../contracts/providers/provider-alpha.descriptor.json';
import scopes from '../../contracts/providers/provider-alpha.verified-scopes.json';
const cases=JSON.parse(readFileSync('../docs/evidence/CODEX-T1-REWORK3-AUDIT/config-cases.json','utf8')) as {name:string;document:unknown;expected:boolean}[];
const cat=createDescriptorCatalog([descriptor],scopes);
describe('Codex fourth review',()=>{for(const c of cases)it(c.name,()=>{const r=validateConfigDocument(c.document,{catalog:cat});console.log(JSON.stringify({name:c.name,result:r}));expect(r.ok).toBe(c.expected)});});
