VERSION ?= 0.17.0
LDFLAGS = -ldflags "-X github.com/mbalazy/pm/internal/version.Version=$(VERSION)"

.PHONY: install vet test check build-pm-linux

install:
	go install -buildvcs=false $(LDFLAGS) ./cmd/pm/

build-pm-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -buildvcs=false $(LDFLAGS) -o bin/pm-linux ./cmd/pm/

vet:
	go vet ./...

test:
	go test ./internal/... -count=1

check: vet test
