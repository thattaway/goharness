.PHONY: build web go test dev-web clean install-service uninstall-service

# Full build: the React app, then the Go binary that embeds it.
build: web go

web: web/node_modules
	npm --prefix web run build

web/node_modules: web/package.json web/package-lock.json
	npm --prefix web ci
	@touch web/node_modules

go:
	go build -o goharness .

test:
	go test -race ./...
	npm --prefix web run typecheck
	npm --prefix web test

# Frontend dev server on :5173 with hot reload; run `./goharness web` alongside it.
dev-web: web/node_modules
	npm --prefix web run dev

clean:
	rm -f goharness
	rm -rf web/dist/assets

# ---- systemd user service ----
# make install-service ADDR=0.0.0.0:8787 PROJECTS_ROOT=$(HOME)/projects WEB_FLAGS=--allow-any-dir
ADDR ?= 0.0.0.0:8787
PROJECTS_ROOT ?= $(HOME)/projects
PROJECT ?= $(CURDIR)
WEB_FLAGS ?=
UNIT_DIR := $(HOME)/.config/systemd/user
# Your PATH, minus per-session tool folders, so the agent's commands find go, npm, conda...
SERVICE_PATH := $(shell printf '%s' "$$PATH" | tr ':' '\n' | grep -v '/\.claude/' | paste -sd:)

install-service: build
	mkdir -p $(UNIT_DIR)
	sed -e 's|@BIN@|$(CURDIR)/goharness|' -e 's|@ADDR@|$(ADDR)|' \
	    -e 's|@PROJECTS_ROOT@|$(PROJECTS_ROOT)|' -e 's|@PROJECT@|$(PROJECT)|' -e 's|@WEB_FLAGS@|$(WEB_FLAGS)|' \
	    -e 's|@PATH@|$(SERVICE_PATH)|' contrib/goharness-web.service.in > $(UNIT_DIR)/goharness-web.service
	systemctl --user daemon-reload
	-systemctl --user try-restart goharness-web
	@echo
	@echo "Installed $(UNIT_DIR)/goharness-web.service. Next:"
	@echo "  systemctl --user enable --now goharness-web   # start now and at login"
	@echo "  loginctl enable-linger $$USER                  # keep it running after you log out"
	@echo "  journalctl --user -u goharness-web -f          # logs (the login link is printed there)"

uninstall-service:
	-systemctl --user disable --now goharness-web
	rm -f $(UNIT_DIR)/goharness-web.service
	systemctl --user daemon-reload
