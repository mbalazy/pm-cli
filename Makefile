VERSION ?= 0.49.1
LDFLAGS = -ldflags "-X github.com/mbalazy/pm/internal/version.Version=$(VERSION)"

.PHONY: install vet staticcheck fmt-check test test-race check build-pm-linux deploy-vps

# staticcheck lives in GOBIN (go install honnef.co/go/tools/cmd/staticcheck@latest),
# which may not be on PATH in every invocation context (hooks, CI) - resolve it.
GOBIN_DIR := $(or $(shell go env GOBIN),$(shell go env GOPATH)/bin)
STATICCHECK := $(or $(shell command -v staticcheck 2>/dev/null),$(GOBIN_DIR)/staticcheck)

# The runner's pm is a plain file and NOTHING syncs it: after a release the mac
# and the runner diverge silently, and you find out from a run behaving like an
# older version rather than from an error. Overridable for a second runner.
VPS_HOST ?= runner
VPS_BIN  ?= /home/runner/go/bin/pm

install:
	go install -buildvcs=false $(LDFLAGS) ./cmd/pm/

build-pm-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -buildvcs=false $(LDFLAGS) -o bin/pm-linux ./cmd/pm/

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
# Matching on the full command line rather than `pgrep -x pm` is deliberate: the
# pm-vps MCP bridge runs as a long-lived `pm mcp` and would otherwise read as a
# live run whenever an acceptance session is open.
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
	@test -x "$(STATICCHECK)" || { echo "staticcheck not found - run: go install honnef.co/go/tools/cmd/staticcheck@latest"; exit 1; }
	"$(STATICCHECK)" ./...

test:
	go test ./... -count=1

test-race:
	go test ./... -race -count=1

check: fmt-check vet staticcheck test
