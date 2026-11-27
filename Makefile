BIN         := bin/gilda
INSTALL_DIR := $(HOME)/.local/bin
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS     := -s -w -X main.version=$(VERSION)

.PHONY: all build test race lint fmt vuln check run repl swarm diagrams diagrams-pdf install clean help

all: build

build:
	@go build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN) ./cmd/gilda

# examples/swarm is its own module, as an embedding app is; its live tests skip here.
SWARM := examples/swarm

test:
	@go test ./...
	@cd $(SWARM) && go test ./...

race:
	@go test -race ./...
	@cd $(SWARM) && go test -race ./...

# Matches what CI would run. `make fmt` applies what `lint` only reports.
lint:
	@test -z "$$(gofmt -l .)" || { gofmt -l .; echo "gofmt: files need formatting"; exit 1; }
	@go vet ./...
	@cd $(SWARM) && go vet ./...

fmt:
	@gofmt -w .

# Needs the network: fetches govulncheck and the vulnerability database.
vuln:
	@go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...
	@cd $(SWARM) && go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...

check: lint race vuln

# Offline smoke tests: no network, no API key.
run: build
	@$(BIN) --mock mock/read-then-answer.json -p "what is this package?"

repl: build
	@$(BIN) --mock mock/say-hi.json

# Needs a provider key and spends money: parallel agents on a real provider.
# SWARM_PROVIDER and SWARM_MODEL pick them; the defaults are the registry's.
swarm:
	@cd $(SWARM) && SWARM_LIVE=1 go test -race -v -timeout 20m ./...

# d2 renders PDF, not SVG, through headless Chromium, which it downloads on first use.
DIAGRAMS := $(wildcard docs/media/*.d2)

diagrams: $(DIAGRAMS:.d2=.svg)

diagrams-pdf: $(DIAGRAMS:.d2=.pdf)

docs/media/%.svg: docs/media/%.d2
	@d2 $< $@

docs/media/%.pdf: docs/media/%.d2
	@d2 $< $@

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
	@echo "swarm     examples/swarm against a real provider; needs a key"
	@echo "diagrams  docs/media/*.d2 as SVG, beside each source"
	@echo "diagrams-pdf  docs/media/*.d2 as PDF, beside each source"
	@echo "install   build, copied to $(INSTALL_DIR)"
	@echo "clean     remove bin/"
