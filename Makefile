GO ?= go
GORELEASER ?= goreleaser

.DEFAULT_GOAL := help

.PHONY: build check clean coverage dist fmt fmt-check help release-check run test test-race vet

build:
	mkdir -p bin
	$(GO) build -o bin/lazycaddy ./cmd/lazycaddy

check: fmt-check test vet

clean:
	$(RM) -r bin dist coverage.out coverage.html

coverage:
	$(GO) test -coverprofile=coverage.out ./...
	$(GO) tool cover -func=coverage.out

dist:
	$(GORELEASER) release --snapshot --clean

fmt:
	$(GO)fmt -w .

fmt-check:
	test -z "$$($(GO)fmt -l .)"

help:
	@printf '%s\n' 'Available targets:'
	@printf '  %-18s %s\n' 'make build' 'Build a local binary in bin/'
	@printf '  %-18s %s\n' 'make check' 'Run formatting, tests and vet'
	@printf '  %-18s %s\n' 'make clean' 'Remove generated local artifacts'
	@printf '  %-18s %s\n' 'make coverage' 'Generate coverage.out and print the summary'
	@printf '  %-18s %s\n' 'make dist' 'Build local release artifacts'
	@printf '  %-18s %s\n' 'make fmt' 'Format Go sources'
	@printf '  %-18s %s\n' 'make fmt-check' 'Verify formatting without changing files'
	@printf '  %-18s %s\n' 'make help' 'Show this help'
	@printf '  %-18s %s\n' 'make release-check' 'Validate the GoReleaser configuration'
	@printf '  %-18s %s\n' 'make run' 'Run lazycaddy locally'
	@printf '  %-18s %s\n' 'make test' 'Run all tests'
	@printf '  %-18s %s\n' 'make test-race' 'Run tests with the race detector'
	@printf '  %-18s %s\n' 'make vet' 'Run Go vet'

release-check:
	$(GORELEASER) check

run:
	$(GO) run ./cmd/lazycaddy

test:
	$(GO) test ./...

test-race:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...
