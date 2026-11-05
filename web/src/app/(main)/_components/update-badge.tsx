"use client";

import * as React from "react";

import Link from "next/link";

import { ArrowUpCircleIcon } from "lucide-react";
import { useTranslations } from "next-intl";

import { api } from "@/lib/api";

/**
 * "New version available" hint in the top bar: checked once on full page load; if an update exists it lights up next to the version number,
 * and clicking it goes straight to the "Version & Updates" card on the system config page.
 *
 * The backend caches its GitHub query result for 30 minutes, so it is safe to query on every mount here
 * -- the unauthenticated GitHub API allows only 60 requests/hour/IP; without that cache, opening a few tabs
 * would exhaust the quota, and then a real update could no longer be checked.
 *
 * Any query failure is silent: the top bar is no place for errors; on the settings page, clicking "Check for updates" shows the reason.
 */
export function UpdateBadge() {
  const t = useTranslations("header");
  const [latest, setLatest] = React.useState("");

  React.useEffect(() => {
    let alive = true;
    api
      .checkUpdate()
      .then((r) => {
        // has_update already includes the "version is comparable" check, so dev builds never show this hint.
        if (alive && r.has_update && r.latest) setLatest(r.latest.replace(/^v(?=\d)/, ""));
      })
      .catch(() => {
        // Silent: being offline / GitHub rate limiting should not pop an error in the top bar.
      });
    return () => {
      alive = false;
    };
  }, []);

  if (!latest) return null;

  return (
    <Link
      href="/system/settings"
      title={t("updateTitle", { latest })}
      className="inline-flex items-center gap-1.5 rounded-full bg-primary px-2.5 py-1 font-medium text-primary-foreground text-xs transition-opacity hover:opacity-90"
    >
      {/* Breathing dot: the top bar has many elements and plain text is easy to overlook; the animation makes it visible at a glance. */}
      <span className="relative flex size-1.5">
        <span className="absolute inline-flex size-full animate-ping rounded-full bg-primary-foreground opacity-75" />
        <span className="relative inline-flex size-1.5 rounded-full bg-primary-foreground" />
      </span>
      <ArrowUpCircleIcon className="size-3.5" />
      <span className="hidden sm:inline">{t("updateBadge", { latest })}</span>
      <span className="sm:hidden">{t("updateBadgeShort")}</span>
    </Link>
  );
}
