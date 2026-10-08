/**
 * Minimal i18n helper: flat key-value dictionaries, zh default, en fallback,
 * key itself as last resort. Time/unit localization is out of scope for this
 * batch (design §4: zh/en copy stays in sync, formats localize later).
 */
import zh from './locales/zh.json';
import en from './locales/en.json';

export type Locale = 'zh' | 'en';

const dictionaries: Record<Locale, Record<string, string>> = {
  zh: zh as Record<string, string>,
  en: en as Record<string, string>,
};

let currentLocale: Locale = 'zh';

export function setLocale(locale: Locale): void {
  currentLocale = locale;
}

export function getLocale(): Locale {
  return currentLocale;
}

export function t(key: string, params?: Record<string, string>): string {
  const message = dictionaries[currentLocale][key] ?? dictionaries.en[key] ?? key;
  return params === undefined
    ? message
    : message.replace(/\{([^}]+)\}/g, (placeholder, name: string) => params[name] ?? placeholder);
}
