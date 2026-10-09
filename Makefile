BIN         := bin/gilda
INSTALL_DIR := $(HOME)/.local/bin
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS     := -s -w -X main.version=$(VERSION)

.PHONY: all build test race lint fmt vuln check run repl install clean help

all: build

build:
	@go build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN) ./cmd/gilda

test:
	@go test ./...

race:
	@go test -race ./...

# Matches what CI would run. `make fmt` applies what `lint` only reports.
lint:
	@test -z "$$(gofmt -l .)" || { gofmt -l .; echo "gofmt: files need formatting"; exit 1; }
	@go vet ./...

fmt:
	@gofmt -w .

# Needs the network: fetches govulncheck and the vulnerability database.
vuln:
	@go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...

check: lint race vuln

# Offline smoke tests: no network, no API key.
run: build
	@$(BIN) --mock mock/read-then-answer.json -p "what is this package?"

repl: build
	@$(BIN) --mock mock/say-hi.json

install: build
	@install -d $(INSTALL_DIR)
	@install -m 755 $(BIN) $(INSTALL_DIR)/gilda
	@echo "installed gilda to $(INSTALL_DIR)"

clean:
	@rm -rf bin

help:
	@echo "build     compile to $(BIN) (default)"
	@echo "test      unit and end-to-end tests"
	@echo "race      tests under the race detector"
	@echo "lint      gofmt check and go vet"
	@echo "fmt       apply gofmt"
	@echo "vuln      govulncheck over every package"
	@echo "check     lint + race + vuln; the full gate"
	@echo "run       one-shot against the mock provider"
	@echo "repl      interactive against the mock provider"
	@echo "install   build, copied to $(INSTALL_DIR)"
	@echo "clean     remove bin/"
