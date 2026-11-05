import { useCallback, useEffect, useMemo, useState } from "react";

import { useTranslations } from "next-intl";

import { api } from "@/lib/api";
import type { Agent } from "@/lib/types";

// The names, descriptions and prompt variable labels of built-in agents (builtin) are DB seed data,
// so they exist only in Chinese (builtinAgents in db/db.go, the
// vulnerability retest agent in server/finding_retests.go). DB values cannot be kept per language and the seed is ON CONFLICT DO NOTHING,
// so it is not even reflected on already-installed instances. So the UI looks up the translation by key in the message files
// (the builtinAgents namespace of web/messages/{ko,en,zh,es,ja,th}.json)
// and shows it, falling back to the DB value if absent. User-created agents always use the DB value.

// retester is seeded in the DB with builtin=false (server FindingRetestAgentKey), but since it is a fixed key planted by the system
// it is included as a translation target. Agents with builtin=true (goals/planner/
// mainagent/worker/auto/pentest) are filtered out by agent.builtin, so they need not be listed here.
const SYSTEM_SEEDED_AGENT_KEYS = new Set(["retester"]);

function isSystemSeeded(agent: { key: string; builtin?: boolean }): boolean {
  return !!agent.builtin || SYSTEM_SEEDED_AGENT_KEYS.has(agent.key);
}

// useAgentLabels is a bundle of helpers that return the names, descriptions and prompt variable labels of built-in agents
// in the current language. Every display point goes through these helpers to get labels.
export function useAgentLabels() {
  const t = useTranslations("builtinAgents");

  // Callers use these functions as useMemo/useCallback dependencies (conversation group / filter computation),
  // so a new bundle is created only when the language (t) changes. A new object on every render would rerun that computation every time.
  return useMemo(() => {
    // name returns the translated name for a built-in agent, otherwise the DB name (agent.name).
    const name = (agent: { key: string; name: string; builtin?: boolean }): string => {
      if (isSystemSeeded(agent) && t.has(`${agent.key}.name`)) return t(`${agent.key}.name`);
      return agent.name;
    };

    // description returns the translated description for a built-in agent, otherwise the DB description (empty string if none).
    // The fallback text for an empty description is attached by the caller as before.
    const description = (agent: { key: string; description?: string; builtin?: boolean }): string => {
      if (isSystemSeeded(agent) && t.has(`${agent.key}.description`)) return t(`${agent.key}.description`);
      return agent.description ?? "";
    };

    // nameByKey is the variant used when only a key is available without an Agent object (conversation grouping, etc.).
    // It takes a lookup function (returning the agent), and returns the translation if found, otherwise the key.
    const nameByKey = (
      key: string,
      lookup: (key: string) => { key: string; name: string; builtin?: boolean } | undefined,
    ): string => {
      const agent = lookup(key);
      // biome-ignore lint/nursery/useNullishCoalescing: an agent with an empty ("") name must still be shown as its key as before, so || is the intended behavior.
      return (agent && name(agent)) || key;
    };

    // varLabel translates the prompt variable label of a built-in agent (e.g. the Chinese seed label of goals
    // for "overall task goal"). If there is none, the DB label (fallback) is used as is.
    const varLabel = (agentKey: string, builtin: boolean, varName: string, fallback: string): string => {
      if ((builtin || SYSTEM_SEEDED_AGENT_KEYS.has(agentKey)) && t.has(`${agentKey}.vars.${varName}`)) {
        return t(`${agentKey}.vars.${varName}`);
      }
      return fallback;
    };

    return { name, description, nameByKey, varLabel };
  }, [t]);
}

// The agent list is only used by several screens (approval records, etc.) for key → display name conversion,
// so it is fetched once and shared at module level. On failure it is fetched again on the next call.
let agentListPromise: Promise<Agent[]> | null = null;
function loadAgentList(): Promise<Agent[]> {
  if (!agentListPromise) {
    agentListPromise = api.agents().catch((err) => {
      agentListPromise = null;
      throw err;
    });
  }
  return agentListPromise;
}

// useAgentNameByKey returns the converter used by screens that have only a key without an agent object (such as
// conv_agent_key in approval / intercept records). Built-in agents show the name in the current language,
// user agents show the DB name, and if it is not in the list or not yet received, the key is shown as is.
export function useAgentNameByKey(): (key: string) => string {
  const labels = useAgentLabels();
  const [agents, setAgents] = useState<Map<string, Agent>>(() => new Map());
  useEffect(() => {
    let alive = true;
    loadAgentList()
      .then((list) => {
        if (alive) setAgents(new Map(list.map((a) => [a.key, a])));
      })
      .catch(() => {
        // If the list cannot be fetched, show the key as is (retry on the next mount).
      });
    return () => {
      alive = false;
    };
  }, []);
  return useCallback((key: string) => labels.nameByKey(key, (k) => agents.get(k)), [labels, agents]);
}
