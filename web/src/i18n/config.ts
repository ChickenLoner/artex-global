// Supported locales and the default. artex-global defaults to English and adds Korean, Chinese, Spanish, Japanese and Thai,
// so six languages can be switched via user settings. Chinese ("zh") is also kept for comparison with the upstream original.
export const LOCALES = ["en", "ko", "zh", "es", "ja", "th"] as const;
export type Locale = (typeof LOCALES)[number];

// The default display language is English. Korean, Chinese, Spanish, Japanese and Thai are languages switched via settings. It can be overridden at build time with
// NEXT_PUBLIC_LOCALE, and at runtime the value the user picks on the settings screen (cookie/localStorage) has the final say.
export const DEFAULT_LOCALE: Locale = "en";

// The native-language name of each locale shown in the language selector. Whatever language is being viewed, a language's own
// name must read in that language, so it is a fixed label rather than a translation target.
export const LOCALE_LABELS: Record<Locale, string> = {
  en: "English",
  ko: "한국어",
  zh: "中文",
  es: "Español",
  ja: "日本語",
  th: "ไทย",
};

// Key that stores the display language the user picked. The UI reads the localStorage value. The same value is also
// written to a cookie, but nothing reads it for now (kept for when the server needs to determine the language).
export const LOCALE_COOKIE = "artex_locale";
export const LOCALE_STORAGE_KEY = "artex_locale";

// Guard that narrows an arbitrary string to a supported locale.
export function isLocale(raw: unknown): raw is Locale {
  return typeof raw === "string" && (LOCALES as readonly string[]).includes(raw);
}

// Decide the active locale at build time. It must be compatible with static export (output: "export" in next.config),
// so it does not use dynamic APIs such as cookies()/headers() and reads only environment variables.
// If NEXT_PUBLIC_LOCALE is empty or outside the supported list, it falls back to the default. This value is used only in the server
// prerender's initial HTML, and the actual user display language is overridden on the client by the cookie.
export function resolveLocale(): Locale {
  const raw = process.env.NEXT_PUBLIC_LOCALE;
  return isLocale(raw) ? raw : DEFAULT_LOCALE;
}
