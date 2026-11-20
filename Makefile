PREFIX ?= $(HOME)/.local
BIN    := bin/claude-relay

.PHONY: build test install install-jetbrains clean

build:
	go build -o $(BIN) ./cmd/claude-relay

test:
	go vet ./...
	go test ./...

install: build
	install -Dm755 $(BIN) $(PREFIX)/bin/claude-relay
	@echo "installed $(PREFIX)/bin/claude-relay"

# Adds the External Tools to every JetBrains IDE config dir (restart the IDE afterwards).
install-jetbrains: install
	$(PREFIX)/bin/claude-relay install-jetbrains

clean:
	rm -rf bin
