import { describe, expect, it } from 'vitest';
import en from '../src/locales/en.json';
import zh from '../src/locales/zh.json';

describe('locale dictionaries', () => {
  it('keep message keys and interpolation parameters aligned', () => {
    const zhKeys = Object.keys(zh).sort();
    const enKeys = Object.keys(en).sort();
    expect(zhKeys).toEqual(enKeys);

    for (const key of zhKeys) {
      const zhParams = [...(zh[key as keyof typeof zh].matchAll(/\{([^}]+)\}/g))].map((match) => match[1]).sort();
      const enParams = [...(en[key as keyof typeof en].matchAll(/\{([^}]+)\}/g))].map((match) => match[1]).sort();
      expect(zhParams, `placeholder mismatch for ${key}`).toEqual(enParams);
    }
  });
});
