"use client";

import type { ReactNode } from "react";
import * as React from "react";

import { NextIntlClientProvider } from "next-intl";

import enMessages from "../../messages/en.json";
import esMessages from "../../messages/es.json";
import jaMessages from "../../messages/ja.json";
import koMessages from "../../messages/ko.json";
import thMessages from "../../messages/th.json";
import zhMessages from "../../messages/zh.json";
import type { Locale } from "./config";
import { useActiveLocale } from "./locale-store";

// Bundle all six languages' messages statically. That way, when the user changes the language on the settings screen,
// every string re-renders in the chosen language on the spot without a rebuild, page reload or async loading
// (also compatible with static export output: "export").
const MESSAGES: Record<Locale, Record<string, unknown>> = {
  en: enMessages as Record<string, unknown>,
  ko: koMessages as Record<string, unknown>,
  zh: zhMessages as Record<string, unknown>,
  es: esMessages as Record<string, unknown>,
  ja: jaMessages as Record<string, unknown>,
  th: thMessages as Record<string, unknown>,
};

// Time zone of the runtime environment (browser / prerender server).
const TIME_ZONE = Intl.DateTimeFormat().resolvedOptions().timeZone;

// Provider responsible for switching language at runtime. It replaces the root layout's NextIntlClientProvider,
// picking and supplying messages for the user's chosen active locale (cookie/localStorage).
export function IntlRuntimeProvider({ children }: Readonly<{ children: ReactNode }>) {
  const locale = useActiveLocale();

  // When the language changes, also match <html lang>. Override the initial lang planted by the root layout on the server
  // with the client's selection, so screen readers and browser translation work in the right language.
  React.useEffect(() => {
    document.documentElement.lang = locale;
  }, [locale]);

  // Unlike the root layout's provider, it does not inherit the server request config, so timeZone is
  // passed directly. If omitted, next-intl logs an ENVIRONMENT_FALLBACK error during prerender.
  return (
    <NextIntlClientProvider locale={locale} messages={MESSAGES[locale]} timeZone={TIME_ZONE}>
      {children}
    </NextIntlClientProvider>
  );
}
