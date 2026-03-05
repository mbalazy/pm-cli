VERSION ?= 0.7.4

.PHONY: install vet test check

install:
	go install -buildvcs=false -ldflags "-X github.com/mbalazy/pm/internal/version.Version=$(VERSION)" ./cmd/pm/

vet:
	go vet ./...

test:
	go test ./internal/... -count=1

check: vet test
