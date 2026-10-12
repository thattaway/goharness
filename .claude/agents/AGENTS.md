# Setup (fresh checkout)
cd web && npm ci
cd .. && go mod download

# Dev server (Vite, listen on 5173)
npm run dev  # or: cd web && npm run dev

# Tests (Vite)
npm run test

# Build
cd web && npm run build  # emits to web/dist/
cd .. && go build -o bin/go-harness

# Lint
cd web && npm run lint 2>/dev/null || true
cd .. && go lint ./...
