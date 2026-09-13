GO ?= go

.PHONY: build test vet fmt generate check clean

build:
	$(GO) build -trimpath -o bin/yamata ./cmd/yamata
	cd examples/reader && $(GO) build -trimpath -o ../../bin/reader .

test:
	$(GO) test ./...
	cd examples/reader && $(GO) test ./...

vet:
	$(GO) vet ./...
	cd examples/reader && $(GO) vet ./...

fmt:
	$(GO) fmt ./...
	cd examples/reader && $(GO) fmt ./...

generate:
	$(GO) generate ./internal/contract

check:
	@test -z "$$(gofmt -l cmd internal examples/reader)" || { gofmt -l cmd internal examples/reader; exit 1; }
	$(MAKE) test vet build

clean:
	rm -f bin/yamata bin/reader
