GO ?= go

.PHONY: fmt test vet check run

fmt:
	$(GO)fmt -w .

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

check:
	test -z "$$($(GO)fmt -l .)"
	$(GO) test ./...
	$(GO) vet ./...

run:
	$(GO) run ./cmd/lazycaddy

