/**
 * i18n locale key consistency helper.
 *
 * ko.json is taken as the baseline, and each locale's "flattened key set" is compared to find both missing and
 * extra keys. The CLI (check-i18n-keys.ts) and the Vitest tests share this pure logic,
 * so the checking rules are defined in only one place.
 */

export interface KeyDiff {
  missing: string[]; // baseline has these keys but target lacks them
  extra: string[]; // baseline lacks these keys but target has them
}

/** Flatten a nested JSON object into a key set of the form "a.b.c". Arrays and primitive values are treated as leaves. */
export function flattenKeys(tree: unknown, prefix = ""): Set<string> {
  const out = new Set<string>();
  if (tree === null || typeof tree !== "object" || Array.isArray(tree)) {
    if (prefix) out.add(prefix);
    return out;
  }
  for (const [key, value] of Object.entries(tree as Record<string, unknown>)) {
    const path = prefix ? `${prefix}.${key}` : key;
    for (const leaf of flattenKeys(value, path)) out.add(leaf);
  }
  return out;
}

/** baseline versus target: returns the missing and extra keys as sorted arrays. */
export function diffKeys(baseline: Set<string>, target: Set<string>): KeyDiff {
  const missing = [...baseline].filter((k) => !target.has(k)).sort();
  const extra = [...target].filter((k) => !baseline.has(k)).sort();
  return { missing, extra };
}

/** true if both missing and extra are empty. */
export function isInSync(diff: KeyDiff): boolean {
  return diff.missing.length === 0 && diff.extra.length === 0;
}
