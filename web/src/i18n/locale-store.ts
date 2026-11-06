"use client";

import * as React from "react";

import { setClientCookie } from "@/lib/cookie.client";
import { getLocalStorageValue, setLocalStorageValue } from "@/lib/local-storage.client";

import { isLocale, LOCALE_COOKIE, LOCALE_STORAGE_KEY, type Locale, resolveLocale } from "./config";

// The display language the user picks is a purely front-end preference, so it is not saved on the server but kept only in
// localStorage (and a cookie). It is not synced with the account, so switching browsers or clearing site
// data returns to the default language. The agent output language is stored separately in the server settings.

function parseLocale(raw: string | null): Locale {
  return isLocale(raw) ? raw : resolveLocale();
}

// The set of subscribers within the same tab. The storage event fires only in "other" tabs, so after changing the
// language in the current tab, subscribers in the same tab must be notified via emit to re-render immediately.
const listeners = new Set<() => void>();

function subscribe(listener: () => void) {
  listeners.add(listener);
  window.addEventListener("storage", listener);
  return () => {
    listeners.delete(listener);
    window.removeEventListener("storage", listener);
  };
}

function emit() {
  for (const listener of listeners) listener();
}

function getSnapshot(): Locale {
  return parseLocale(getLocalStorageValue(LOCALE_STORAGE_KEY));
}

// The server has no localStorage, so first render the build default locale, and after hydration
// getSnapshot corrects it to the user's chosen value.
function getServerSnapshot(): Locale {
  return resolveLocale();
}

// A hook that subscribes to the active display language. When the language changes, components using this hook re-render.
export function useActiveLocale(): Locale {
  return React.useSyncExternalStore(subscribe, getSnapshot, getServerSnapshot);
}

// Change the display language. Writes to the cookie and localStorage and notifies same-tab subscribers. Kept for one year so
// the choice persists on the next visit.
export function setActiveLocale(locale: Locale) {
  if (!isLocale(locale)) return;
  setLocalStorageValue(LOCALE_STORAGE_KEY, locale);
  setClientCookie(LOCALE_COOKIE, locale, 365);
  emit();
}
