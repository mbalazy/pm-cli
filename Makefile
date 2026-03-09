VERSION ?= 0.7.4
LDFLAGS = -ldflags "-X github.com/mbalazy/pm/internal/version.Version=$(VERSION)"

.PHONY: install install-sync vet test check build-pm-linux build-sync-linux

install:
	go install -buildvcs=false $(LDFLAGS) ./cmd/pm/

install-sync:
	go install -buildvcs=false $(LDFLAGS) ./cmd/pm-sync/

build-pm-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -buildvcs=false $(LDFLAGS) -o bin/pm-linux ./cmd/pm/

build-sync-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -buildvcs=false $(LDFLAGS) -o bin/pm-sync-linux ./cmd/pm-sync/

vet:
	go vet ./...

test:
	go test ./internal/... -count=1

check: vet test
