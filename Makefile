GO ?= go
PYTHON ?= python3
export GOWORK := off
export PYTHONDONTWRITEBYTECODE := 1

.PHONY: build test vet fmt generate check demo demo-clean verify clean

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

demo: build
	$(PYTHON) scripts/demo.py

demo-clean:
	$(PYTHON) scripts/demo.py --clean

verify: check
	$(PYTHON) tests/walkthrough.py
	$(PYTHON) tests/operations.py

clean:
	rm -f bin/yamata bin/reader
