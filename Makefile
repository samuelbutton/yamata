GO ?= go

.PHONY: build test vet fmt check clean

build:
	$(GO) build -trimpath -o bin/yamata ./cmd/yamata

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

fmt:
	$(GO) fmt ./...

check:
	@test -z "$$(gofmt -l cmd internal)" || { gofmt -l cmd internal; exit 1; }
	$(MAKE) test vet build

clean:
	rm -f bin/yamata
