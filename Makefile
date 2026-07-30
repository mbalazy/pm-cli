VERSION ?= 0.29.2
LDFLAGS = -ldflags "-X github.com/mbalazy/pm/internal/version.Version=$(VERSION)"

.PHONY: install vet staticcheck test check build-pm-linux

# staticcheck lives in GOBIN (go install honnef.co/go/tools/cmd/staticcheck@latest),
# which may not be on PATH in every invocation context (hooks, CI) - resolve it.
GOBIN_DIR := $(or $(shell go env GOBIN),$(shell go env GOPATH)/bin)
STATICCHECK := $(or $(shell command -v staticcheck 2>/dev/null),$(GOBIN_DIR)/staticcheck)

install:
	go install -buildvcs=false $(LDFLAGS) ./cmd/pm/

build-pm-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -buildvcs=false $(LDFLAGS) -o bin/pm-linux ./cmd/pm/

vet:
	go vet ./...

staticcheck:
	@test -x "$(STATICCHECK)" || { echo "staticcheck not found - run: go install honnef.co/go/tools/cmd/staticcheck@latest"; exit 1; }
	"$(STATICCHECK)" ./...

test:
	go test ./internal/... -count=1

check: vet staticcheck test
