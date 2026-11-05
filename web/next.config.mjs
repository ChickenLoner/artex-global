import createNextIntlPlugin from "next-intl/plugin";

import { fileURLToPath } from "node:url";

// Static export: `NEXT_EXPORT=1 next build` produces a plain static directory at web/out that can be dropped straight into
// nginx web root directory to run. Development (next dev) leaves this variable unset, keeping the /api proxy and hot reload.
const isExport = process.env.NEXT_EXPORT === "1";
// Vercel demo: the whole site runs on mock data with no backend, so no /api proxy is needed.
const isMock = process.env.NEXT_PUBLIC_MOCK === "1";

/** @type {import('next').NextConfig} */
const nextConfig = {
  // Avoid a parent directory's lockfile affecting root inference and asset path generation.
  turbopack: { root: fileURLToPath(new URL(".", import.meta.url)) },
  reactCompiler: true,
  // Allow dev resources (HMR) to be accessed from LAN IPs; add or remove as needed.
  // dev In dev, allow any IPv4 origin to access /_next/* and HMR (unaffected when the LAN IP changes).
  // Note: for security Next forbids a bare "*", so use segmented wildcards; "*.*.*.*" matches any IPv4.
  allowedDevOrigins: ["*.*.*.*"],
  compiler: {
    removeConsole: process.env.NODE_ENV === "production",
  },
  ...(isExport
    ? {
        // Pure static export: no Node runtime; images are not optimized; each route emits <route>/index.html.
        output: "export",
        images: { unoptimized: true },
        trailingSlash: true,
      }
    : isMock
      ? {
          // Vercel mock demoVercel mock demo: no backend, so no /api proxy is needed.
          images: { unoptimized: true },
        }
      : {
          // Dev: proxy /api/* to the Go backend (default :8787, overridable via AUTOPENTEST_API).
          async rewrites() {
            const backend = process.env.AUTOPENTEST_API ?? "http://localhost:8787";
            return [{ source: "/api/:path*", destination: `${backend}/api/:path*` }];
          },
        }),
};

// next-intl plugin. Request config lives in src/i18n/request.ts. i18n path routing
// is not used, so no middleware is added (compatible with static export output: "export").
const withNextIntl = createNextIntlPlugin("./src/i18n/request.ts");

export default withNextIntl(nextConfig);
