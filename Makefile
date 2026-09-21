VERSION ?= 0.66.0
LDFLAGS = -ldflags "-X github.com/mbalazy/pm-cli/internal/version.Version=$(VERSION)"

.PHONY: install install-full vet staticcheck fmt-check test test-race check build-pm-linux deploy-vps web-install web-check web

# staticcheck lives in GOBIN (go install honnef.co/go/tools/cmd/staticcheck@v0.6.1),
# which may not be on PATH in every invocation context (hooks, CI) - resolve it.
# The version is the one .github/workflows/ci.yml installs: an unpinned @latest
# locally means a check that passes here can still fail in CI, and the other way
# round. Bump both together.
GOBIN_DIR := $(or $(shell go env GOBIN),$(shell go env GOPATH)/bin)
STATICCHECK := $(or $(shell command -v staticcheck 2>/dev/null),$(GOBIN_DIR)/staticcheck)

install:
	go install -buildvcs=false $(LDFLAGS) ./cmd/pm/

build-pm-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -buildvcs=false $(LDFLAGS) -o bin/pm-linux ./cmd/pm/

# --- personal runner (a remote box that runs executor batches) ---
# Nothing syncs the runner's pm binary: after a release the laptop and the
# runner diverge silently, and you find out from a run behaving like an older
# version rather than from an error. Override both for your own host.
VPS_HOST ?= runner
VPS_BIN  ?= /home/runner/go/bin/pm

# Cross-compile and install onto the runner. Two guards, both skipped by FORCE=1:
#
#   dirty tree - a binary built from uncommitted code cannot be traced back to
#   anything, and `pm --version` reports the same string either way. That is the
#   silence this target exists to end, so it refuses rather than warns.
#
#   live run   - not for the file's sake (mv is a rename, so a running manager
#   keeps its own inode and is untouched) but so the version cannot change under
#   a half-finished epic whose re-entrant logic assumes one manager.
#
# The pgrep pattern is bracketed ([p]m) so it cannot match the ssh command line
# carrying it - without that the check finds itself and every deploy "fails".
# Matching on the full command line rather than `pgrep -x pm` is deliberate: a
# long-lived `pm mcp` server on the runner would otherwise read as a live run
# whenever an editor session is connected to it.
#
# It keeps NO backup of the replaced binary, deliberately. A backup is for
# something you cannot recreate, and this binary is `git checkout <sha> && make
# deploy-vps` away - twenty seconds. The version that was tried first copied the
# outgoing binary aside, and deploying the same version twice promptly
# overwrote that copy with an identical one, which is a way back to nowhere.
# The build is inside the recipe, not a prerequisite: make runs prerequisites
# first, so a refused deploy would still pay for a cross-compile it throws away.
deploy-vps:
	@set -e; \
	if [ -z "$(FORCE)" ] && [ -n "$$(git status --porcelain)" ]; then \
		echo "refusing: dirty tree - the deployed binary could not be traced to a commit (FORCE=1 overrides)" >&2; exit 1; \
	fi; \
	if [ -z "$(FORCE)" ] && ssh $(VPS_HOST) 'pgrep -f "[p]m (work|run-epic)" >/dev/null'; then \
		echo "refusing: an executor run is live on $(VPS_HOST) (FORCE=1 overrides)" >&2; exit 1; \
	fi; \
	$(MAKE) build-pm-linux; \
	echo "deploying $(VERSION) ($$(git rev-parse --short HEAD)) to $(VPS_HOST)"; \
	scp -q bin/pm-linux $(VPS_HOST):$(VPS_BIN).new; \
	ssh $(VPS_HOST) 'chmod +x $(VPS_BIN).new && mv $(VPS_BIN).new $(VPS_BIN)'; \
	printf 'mac  '; "$(GOBIN_DIR)/pm" --version; \
	printf 'vps  '; ssh $(VPS_HOST) '$(VPS_BIN) --version'

fmt-check:
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "gofmt needed on:" >&2; \
		echo "$$unformatted" >&2; \
		exit 1; \
	fi

vet:
	go vet ./...

staticcheck:
	@test -x "$(STATICCHECK)" || { echo "staticcheck not found - run: go install honnef.co/go/tools/cmd/staticcheck@v0.6.1"; exit 1; }
	"$(STATICCHECK)" ./...

test:
	go test ./... -count=1

test-race:
	go test ./... -race -count=1

check: fmt-check vet staticcheck test

# --- web (the React cockpit under web/, embedded by internal/server) ---
#
# Deliberately NOT part of `check` or `install`: the pre-commit hook and the Go
# build must stay fast and node-free. `go build` works without a bundle (the
# .gitkeep in internal/server/dist keeps the embed dir present); `pm serve` then
# shows a placeholder page until `make web` has run.

# npm ci needs web/package-lock.json and installs exactly what it pins.
web-install:
	cd web && npm ci

# lint (oxlint + prettier --check), tsc, vitest - the same set CI's web job runs.
web-check:
	cd web && npm run lint && npm run typecheck && npm test -- --run

# Build the bundle into internal/server/dist (requires `make web-install` once).
# Vite empties the dir, which takes the versioned .gitkeep with it - restore it
# so a build never shows up as a deleted file in git status.
web:
	cd web && npm run build
	touch internal/server/dist/.gitkeep

# Bundle + binary: the one command that ships the cockpit.
install-full: web install
