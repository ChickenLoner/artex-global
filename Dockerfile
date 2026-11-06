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
# The base image is pinned by digest (the multi-arch index, so amd64 and arm64 both resolve) for reproducible,
# tamper-evident builds. To take base-image security updates, look up the new digest of
# python:3.12-slim-bookworm (e.g. `docker buildx imagetools inspect python:3.12-slim-bookworm`) and replace it here.
FROM python:3.12-slim-bookworm@sha256:935c3c5b12357a7e10caa9c1e6ca5692344d97cf229832ef584be2cec9fc03df
ARG TARGETARCH
# Node.js is installed from the official release tarball at a pinned version, verified against a pinned SHA-256
# (no `curl | bash` installer). The default apt nodejs on bookworm is 18 and Playwright requires >=20; Node 20 reached
# end-of-life on 2026-04-30, so this uses the Node 22 LTS line. To upgrade: pick the new version on nodejs.org/dist,
# take both hashes for node-v<version>-linux-{x64,arm64}.tar.xz from that release's SHASUMS256.txt, and update the three ARGs.
ARG NODE_VERSION=22.23.4
ARG NODE_SHA256_AMD64=1805bcd35e6815559bd2eb97db438d3fdb0eb3a5e6a977b7c1badca76337e9ce
ARG NODE_SHA256_ARM64=6287aa46b6c375d816e63ae66d024fbbfd70404825aabdfdf898a970871d2f4b
# Commonly used tools: ripgrep / curl / vim plus a bundle of tools frequently used for recon (add or remove as needed).
# These come from Debian bookworm's own signed apt repositories; their versions follow the pinned base image's suite.
RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates ripgrep curl wget vim git jq unzip xz-utils \
      dnsutils iputils-ping netcat-openbsd inetutils-telnet whois nmap \
    && case "${TARGETARCH}" in \
         amd64) node_arch=x64;   node_sha="${NODE_SHA256_AMD64}" ;; \
         arm64) node_arch=arm64; node_sha="${NODE_SHA256_ARM64}" ;; \
         *) echo "unsupported TARGETARCH: ${TARGETARCH}" >&2; exit 1 ;; \
       esac \
    && node_tar="node-v${NODE_VERSION}-linux-${node_arch}.tar.xz" \
    && curl -fsSL -o "/tmp/${node_tar}" "https://nodejs.org/dist/v${NODE_VERSION}/${node_tar}" \
    && echo "${node_sha}  /tmp/${node_tar}" | sha256sum -c - \
    && tar -xJf "/tmp/${node_tar}" -C /usr/local --strip-components=1 --no-same-owner \
         --exclude='CHANGELOG.md' --exclude='README.md' \
    && rm -f "/tmp/${node_tar}" \
    && node --version && npm --version \
    && rm -rf /var/lib/apt/lists/*
# Preinstall the Playwright MCP and CLI globally (so nothing is downloaded over the network via npx at runtime),
# at pinned versions instead of @latest. The MCP and the CLI each bundle their own playwright-core and both depend on
# the exact Playwright build below; the globally installed `playwright` (used only to download the browser) MUST be the same build,
# otherwise `playwright install` fetches a different Chromium revision than the one the MCP/CLI look for.
# To upgrade, bump the three ARGs together: run `npm view @playwright/mcp@<version> dependencies` and set PLAYWRIGHT_VERSION
# to the `playwright` version it lists.
# @playwright/mcp: the browser MCP runs directly with `npx @playwright/mcp` (installed globally).
# @playwright/cli: provides playwright-cli; after installation, verify it runs with --help.
# `playwright install --with-deps` preinstalls chromium and its system dependencies, so the MCP/CLI work on first start
# without downloading the browser over the network.
ARG PLAYWRIGHT_MCP_VERSION=0.0.84
ARG PLAYWRIGHT_CLI_VERSION=0.1.23
ARG PLAYWRIGHT_VERSION=1.65.0-alpha-2026-10-30
RUN npm install -g "@playwright/mcp@${PLAYWRIGHT_MCP_VERSION}" "@playwright/cli@${PLAYWRIGHT_CLI_VERSION}" "playwright@${PLAYWRIGHT_VERSION}" \
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
