# syntax=docker/dockerfile:1
#
# Runtime-only image (nothing is compiled inside the image): install only the commonly used tools and drop in a **precompiled single Linux binary**.
# The binary is cross-compiled by the CI binaries job (pure Go, no QEMU) and placed per target architecture
# at dist/<TARGETARCH>/artex in the build context. This way, in a multi-arch build only the apt layer needs to be
# emulated for arm64, and the Next/Go compilation is no longer emulated, which is much faster.
#
# When building the image manually on a local machine, prepare the binary yourself first:
#   cd web && npm run build:static && cd ..
#   mkdir -p server/webui && cp -r web/out server/webui/dist
#   CGO_ENABLED=0 GOARCH=amd64 go build -tags embedui -o dist/amd64/artex ./cmd/artex
#   docker build -t artex:local .
FROM python:3.12-slim-bookworm
ARG TARGETARCH
# Commonly used tools: ripgrep / curl / vim plus a bundle of tools frequently used for recon (add or remove as needed).
# Node 20.x is installed from NodeSource: the default apt nodejs on bookworm is 18, and Playwright requires >=20.
RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates ripgrep curl wget vim git jq unzip \
      dnsutils iputils-ping netcat-openbsd inetutils-telnet whois nmap \
    && curl -fsSL https://deb.nodesource.com/setup_20.x | bash - \
    && apt-get install -y --no-install-recommends nodejs \
    && rm -rf /var/lib/apt/lists/*
# Preinstall the Playwright MCP and CLI globally (so nothing is downloaded over the network via npx at runtime).
# @playwright/mcp: the browser MCP runs directly with `npx @playwright/mcp` (installed globally, so -y/@latest is not needed).
# @playwright/cli: provides playwright-cli; after installation, verify it runs with --help.
# Next install playwright (which provides browser management) and use --with-deps to preinstall chromium and its system dependencies.
# This way the MCP/CLI inside the container can be used immediately on first start, without downloading the browser over the network.
RUN npm install -g @playwright/mcp@latest @playwright/cli@latest playwright@latest \
    && playwright-cli --help \
    && playwright install --with-deps chromium \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /app
# Precompiled binary for the matching architecture (dist/amd64/artex or dist/arm64/artex)
COPY dist/${TARGETARCH}/artex /app/artex
# Supervisor start script: after the process exits it decides from the exit code whether to relaunch, and the one-click update in the UI completes the swap through this script.
# This script also forwards SIGTERM to artex: docker stop sends the signal only to PID 1,
# so if it is not forwarded, artex never receives the signal, cannot shut down gracefully, and is killed with SIGKILL after 10 seconds.
COPY start.sh /app/start.sh
RUN chmod +x /app/artex /app/start.sh
COPY skills/ /app/skills/
# Persistence point for data/ (SQLite + jwt.key)
VOLUME ["/app/data"]
EXPOSE 8787 8788
ENTRYPOINT ["/app/start.sh"]
CMD ["-addr", ":8787", "-proxy", ":8788"]
