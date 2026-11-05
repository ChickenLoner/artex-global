import { DEFAULT_LOCALE, type Locale, resolveLocale } from "@/i18n/config";

import packageJson from "../../package.json";

const currentYear = new Date().getFullYear();

// Browser tab title and search-engine meta description. They go into layout.tsx's static metadata and are rendered into the
// <title> and <meta name="description"> of every page, so they branch across the six languages according to the build default locale.
// zh keeps the original text unchanged for upstream comparison. The title uses a colon instead of an em dash,
// in the form "Product name: description". This meta is a static build-time value, so it does not react to a language
// switch at runtime (acceptable, as it is just one tab-title line).
const META_BY_LOCALE: Record<Locale, { title: string; description: string }> = {
  en: {
    title: "ARTEX: Autonomous Penetration Testing Console",
    description: "An LLM-driven system console for autonomous penetration testing",
  },
  ko: {
    title: "ARTEX: 자율 침투 테스트 콘솔",
    description: "LLM 기반으로 자율 침투 테스트를 수행하는 시스템 콘솔",
  },
  zh: {
    title: "ARTEX — 自主渗透测试控制台",
    description: "LLM 驱动的自主渗透测试系统控制台",
  },
  es: {
    title: "ARTEX: Consola de Pruebas de Penetración Autónomas",
    description: "Una consola de sistema impulsada por LLM para pruebas de penetración autónomas",
  },
  ja: {
    title: "ARTEX: 自律型ペネトレーションテストコンソール",
    description: "LLM を活用して自律的にペネトレーションテストを行うシステムコンソール",
  },
  th: {
    title: "ARTEX: คอนโซลทดสอบเจาะระบบอัตโนมัติ",
    description: "คอนโซลระบบที่ขับเคลื่อนด้วย LLM สำหรับการทดสอบเจาะระบบอัตโนมัติ",
  },
};

export const APP_CONFIG = {
  name: "ARTEX",
  version: packageJson.version,
  copyright: `© ${currentYear}, ARTEX.`,
  meta: META_BY_LOCALE[resolveLocale()] ?? META_BY_LOCALE[DEFAULT_LOCALE],
};
