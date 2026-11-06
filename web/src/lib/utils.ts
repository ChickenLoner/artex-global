import { type ClassValue, clsx } from "clsx";
import { twMerge } from "tailwind-merge";

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

// Helper for the onInteractOutside close decision of drawers/dialogs (Sheet/Dialog).
//
// Background: Radix popups inside a drawer (Select dropdown, DropdownMenu, Popover, etc.) are portaled outside
// the drawer. When a popup is open and the user clicks the overlay / outside the drawer to dismiss it, this one pointerdown is handled simultaneously by both the Select and Sheet
// DismissableLayer components at the same time; Select closes first and, being a discrete event, React flushes synchronously, so
// by the time it is the Sheet handler's turn the popup's data-state has long since flipped to closed - detecting whether a popup is open "right now"
// is inherently unreliable (verified in practice).
//
// Correct approach: Radix's pointerdown listener is in the bubble phase; in the capture phase (earlier than it) we first record
// "whether any popup is open at this moment", and onInteractOutside then reads that recorded value to decide whether to let the close through.
function isRadixOverlayOpenNow(): boolean {
  if (typeof document === "undefined") return false;
  return !!document.querySelector(
    [
      "[data-slot='select-trigger'][data-state='open']",
      "[data-slot='select-content'][data-state='open']",
      "[role='listbox'][data-state='open']",
      "[data-radix-popper-content-wrapper]",
      "[aria-expanded='true'][data-state='open']",
    ].join(","),
  );
}

let overlayOpenAtLastPointerDown = false;
if (typeof document !== "undefined") {
  document.addEventListener(
    "pointerdown",
    () => {
      overlayOpenAtLastPointerDown = isRadixOverlayOpenNow();
    },
    true, // capture:record before Radix's bubble-phase pointerdown handler
  );
}

// radixOverlayWasOpenAtPointerDown returns whether a Radix popup was open when the most recent pointerdown happened.
// Drawers/dialogs use this: when a popup is open and the overlay is clicked → close only the popup, not themselves.
export function radixOverlayWasOpenAtPointerDown(): boolean {
  return overlayOpenAtLastPointerDown;
}

// copyText writes text to the clipboard and returns whether it succeeded.
// Background: navigator.clipboard is available only in secure contexts (HTTPS / localhost); when accessed via IP + HTTP
// it is undefined, and then we degrade to execCommand("copy").
export async function copyText(text: string): Promise<boolean> {
  if (navigator.clipboard && window.isSecureContext) {
    try {
      await navigator.clipboard.writeText(text);
      return true;
    } catch {
      // Continue with the degraded approach
    }
  }
  try {
    const textarea = document.createElement("textarea");
    textarea.value = text;
    textarea.style.position = "fixed";
    textarea.style.left = "-9999px";
    textarea.style.top = "0";
    document.body.appendChild(textarea);
    textarea.focus();
    textarea.select();
    const ok = document.execCommand("copy");
    document.body.removeChild(textarea);
    return ok;
  } catch {
    return false;
  }
}

export const getInitials = (str: string): string => {
  if (typeof str !== "string" || !str.trim()) return "?";

  return (
    str
      .trim()
      .split(/\s+/)
      .filter(Boolean)
      .map((word) => word[0])
      .join("")
      .toUpperCase() || "?"
  );
};

export function formatCurrency(
  amount: number,
  opts?: {
    currency?: string;
    locale?: string;
    minimumFractionDigits?: number;
    maximumFractionDigits?: number;
    noDecimals?: boolean;
  },
) {
  const { currency = "USD", locale = "en-US", minimumFractionDigits, maximumFractionDigits, noDecimals } = opts ?? {};

  const formatOptions: Intl.NumberFormatOptions = {
    style: "currency",
    currency,
    minimumFractionDigits: noDecimals ? 0 : minimumFractionDigits,
    maximumFractionDigits: noDecimals ? 0 : maximumFractionDigits,
  };

  return new Intl.NumberFormat(locale, formatOptions).format(amount);
}
