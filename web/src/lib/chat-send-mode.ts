"use client";

import * as React from "react";

import { useTranslations } from "next-intl";

import { getLocalStorageValue, setLocalStorageValue } from "@/lib/local-storage.client";

// The send / newline key combination of the session input box. It is a pure frontend preference, so it is stored only in localStorage
// and not on the server, and is not synced with the account. So after switching browsers
// it must be set again. See issue #39: 0.3.2 changed Ctrl+Enter send to Enter send,
// and here the old key combination is revived as an option.
export type ChatSendMode = "enter" | "ctrl-enter";

export const CHAT_SEND_MODE_KEY = "artex_chat_send_mode";
export const DEFAULT_CHAT_SEND_MODE: ChatSendMode = "enter";

// Options used by the send-mode dropdown on the settings screen. Labels vary with the locale, so they are returned from a hook
// rather than a module constant (hardcoding Korean in a pure module would break the zh locale
// with Korean text). value is a stable identifier, used as is for the localStorage key and behavior decisions.
export function useChatSendModeOptions(): { value: ChatSendMode; label: string }[] {
  const t = useTranslations("chatSendMode.option");
  return [
    { value: "enter", label: t("enter") },
    { value: "ctrl-enter", label: t("ctrlEnter") },
  ];
}

function parseMode(raw: string | null): ChatSendMode {
  return raw === "ctrl-enter" || raw === "enter" ? raw : DEFAULT_CHAT_SEND_MODE;
}

// Set of subscribers within the same tab. The storage event of localStorage fires only in "other" tabs,
// so after changing the setting in the current tab, the input box in the same tab must be notified via emit. Otherwise
// it takes a refresh to take effect.
const listeners = new Set<() => void>();

function subscribe(listener: () => void) {
  listeners.add(listener);
  window.addEventListener("storage", listener);
  return () => {
    listeners.delete(listener);
    window.removeEventListener("storage", listener);
  };
}

// It returns a string literal, so Object.is compares by value and useSyncExternalStore does not fall into an infinite
// loop.
function getSnapshot(): ChatSendMode {
  return parseMode(getLocalStorageValue(CHAT_SEND_MODE_KEY));
}

// The server has no localStorage, so it renders the default first, and after hydration getSnapshot
// corrects the value.
function getServerSnapshot(): ChatSendMode {
  return DEFAULT_CHAT_SEND_MODE;
}

export function useChatSendMode(): ChatSendMode {
  return React.useSyncExternalStore(subscribe, getSnapshot, getServerSnapshot);
}

export function setChatSendMode(mode: ChatSendMode) {
  setLocalStorageValue(CHAT_SEND_MODE_KEY, mode);
  for (const listener of listeners) listener();
}

// shouldSubmitOnKey decides whether one key press amounts to a submit.
// isComposing / keyCode 229 means an input method (Korean, Chinese, etc.) is composing characters,
// so it must always pass through. Otherwise confirming a character with Enter would submit by mistake.
// enter mode excludes only Shift, keeping the 0.3.2 behavior identical down to the last character (the feel
// does not change for users who did not change the setting). ctrl-enter mode accepts both Ctrl and Cmd (macOS)
// together.
export function shouldSubmitOnKey(e: React.KeyboardEvent, mode: ChatSendMode): boolean {
  if (e.key !== "Enter") return false;
  if (e.nativeEvent.isComposing || e.nativeEvent.keyCode === 229) return false;
  if (mode === "ctrl-enter") return e.ctrlKey || e.metaKey;
  return !e.shiftKey;
}
