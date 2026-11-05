#!/usr/bin/env bash
# ARTEX install script: (1) everything in Docker  (2) compile and run locally
set -euo pipefail
cd "$(cd "$(dirname "$0")" && pwd)"

info(){ printf '\033[36m[*]\033[0m %s\n' "$*"; }
ok(){   printf '\033[32m[+]\033[0m %s\n' "$*"; }
warn(){ printf '\033[33m[!]\033[0m %s\n' "$*"; }
die(){  printf '\033[31m[x]\033[0m %s\n' "$*" >&2; exit 1; }
ask(){  local p="$1" d="${2:-}" a; read -rp "$p${d:+ [$d]}: " a; echo "${a:-$d}"; }
rand(){ head -c 18 /dev/urandom | base64 | tr -dc 'A-Za-z0-9' | head -c 24; }

# -- Detect docker environment / auto-install --------------------
ensure_docker(){
  if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
    ok "Found docker and docker compose"; return
  fi
  warn "docker / docker compose not found"
  case "$(uname -s)" in
    Linux)
      if [ "$(ask 'Install Docker automatically? (y/n)' y)" = y ]; then
        curl -fsSL https://get.docker.com | sh
        sudo usermod -aG docker "$USER" || true
        ok "Docker installation finished (the user group change takes effect without sudo only after you log in again)"
      else
        die "Please install docker manually and run this again"
      fi ;;
    Darwin) die "On macOS, please install Docker Desktop: https://www.docker.com/products/docker-desktop/" ;;
    *)      die "Please install docker manually and run this again" ;;
  esac
}

# -- (1) Everything in Docker --------------------------------
install_docker(){
  ensure_docker
  if [ ! -f .env ]; then
    cp .env.example .env 2>/dev/null || true
    local pw key
    pw="$(ask 'Postgres password (press Enter to generate a random one)' "$(rand)")"
    key="$(ask 'ANTHROPIC_API_KEY (may be left empty; set it later in the UI)' '')"
    sed -i.bak "s|^POSTGRES_PASSWORD=.*|POSTGRES_PASSWORD=${pw}|" .env
    sed -i.bak "s|^ANTHROPIC_API_KEY=.*|ANTHROPIC_API_KEY=${key}|" .env
    rm -f .env.bak
    ok "Created the .env file (POSTGRES_PASSWORD set)"
  else
    info "Using the existing .env file as is"
  fi
  info "Pulling the image and starting..."
  docker compose pull || true
  if ! docker compose up -d; then
    warn "Could not pull or start the image ghcr.io/jiwoochris/artex-global (check that ARTEX_TAG in .env is a published tag and that the network works)"
    warn "You can run this script again and choose \"2) Run locally (go compile)\", or build via the README section \"Compile a single binary from source\""
    die "Failed to start Docker Compose (everything in Docker)"
  fi
  ok "Startup finished -> http://localhost:8787"
  info "On first access you must enter the \"setup token\" on the /setup screen to create the admin password"
  info "To find the setup token: docker compose logs artex | grep -F '[auth]'   (the default published address is 127.0.0.1; change it with ARTEX_BIND in .env)"
  info "View logs: docker compose logs -f artex"
}

# -- (2) Compile and run locally ------------------------------
install_local(){
  echo "Database setup:"
  echo "  1) Connect to an existing PostgreSQL"
  echo "  2) Start a PostgreSQL with Docker (requires docker)"
  case "$(ask 'Choice' 1)" in
    2)
      ensure_docker
      local pw; pw="$(ask 'Postgres password (press Enter for a random one)' "$(rand)")"
      docker run -d --name artex-pg -p 5432:5432 \
        -e POSTGRES_USER=artex -e POSTGRES_PASSWORD="$pw" -e POSTGRES_DB=artex \
        -v artex-pg:/var/lib/postgresql/data postgres:16-alpine
      DB_HOST=127.0.0.1 DB_PORT=5432 DB_USER=artex DB_PASS="$pw" DB_NAME=artex DB_SSL=disable ;;
    *)
      DB_HOST="$(ask 'Database host' 127.0.0.1)"
      DB_PORT="$(ask 'Port' 5432)"
      DB_USER="$(ask 'User' artex)"
      DB_PASS="$(ask 'Password' '')"
      DB_NAME="$(ask 'Database name' artex)"
      DB_SSL="$(ask 'sslmode (disable/require)' disable)" ;;
  esac

  # Generate config.json
  cat > config.json <<JSON
{
  "database": {
    "host": "${DB_HOST}",
    "port": ${DB_PORT},
    "user": "${DB_USER}",
    "password": "${DB_PASS}",
    "dbname": "${DB_NAME}",
    "sslmode": "${DB_SSL}"
  }
}
JSON
  ok "Created the config.json file"

  # Check the go environment
  command -v go >/dev/null 2>&1 || die "Go not found. Please install Go (>=1.26) first: https://go.dev/dl/"
  ok "Go: $(go version)"

  # To embed the frontend, the static output must be built with node
  if command -v npm >/dev/null 2>&1; then
    info "Building the static frontend output..."
    ( cd web && npm ci && npm run build:static )
    rm -rf server/webui/dist && mkdir -p server/webui && cp -r web/out server/webui/dist
    info "Compiling a single binary with the embedded frontend..."
    CGO_ENABLED=0 go build -tags embedui -trimpath -o artex ./cmd/artex
  else
    warn "npm not found: compiling only the backend without the embedded frontend (run the frontend separately with npm run dev)"
    CGO_ENABLED=0 go build -o artex ./cmd/artex
  fi
  ok "Compile finished -> ./artex"

  info "Starting... (Ctrl-C to stop). The default bind is 127.0.0.1:8787; on first access enter the [auth] setup token from the log below on the /setup screen"
  ./artex
}

echo "=============================="
echo "  ARTEX install"
echo "  1) Install everything in Docker"
echo "  2) Run locally (go compile)"
echo "=============================="
case "$(ask 'Choice' 1)" in
  1) install_docker ;;
  2) install_local ;;
  *) die "Invalid choice" ;;
esac
