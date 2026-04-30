APP := loc
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
PRIMARY_INSTALL := /usr/local/bin/$(APP)
FALLBACK_INSTALL := $(HOME)/.local/bin/$(APP)

.PHONY: build install release uninstall clean test-release-script

build:
	@mkdir -p bin
	CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o bin/$(APP) ./cmd/$(APP)

install: build
	@set -e; \
	if [ -d "$$(dirname "$(PRIMARY_INSTALL)")" ] && [ -w "$$(dirname "$(PRIMARY_INSTALL)")" ]; then \
		target="$(PRIMARY_INSTALL)"; \
	else \
		target="$(FALLBACK_INSTALL)"; \
		mkdir -p "$$(dirname "$$target")"; \
	fi; \
	cp bin/$(APP) "$$target"; \
	chmod +x "$$target"; \
	echo "Installed $(APP) to $$target"

release:
	@mkdir -p dist
	@set -e; \
	for os in linux darwin; do \
		for arch in amd64 arm64; do \
			output="dist/$(APP)-$$os-$$arch"; \
			echo "Building $$output"; \
			GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o "$$output" ./cmd/$(APP); \
		done; \
	done

uninstall:
	@set -e; \
	removed=0; \
	for target in "$(PRIMARY_INSTALL)" "$(FALLBACK_INSTALL)"; do \
		if [ -e "$$target" ]; then \
			if rm -f "$$target" 2>/dev/null; then \
				echo "Removed $$target"; \
				removed=1; \
			else \
				echo "Could not remove $$target"; \
			fi; \
		fi; \
	done; \
	if [ "$$removed" = "0" ]; then \
		echo "No installed $(APP) binary found"; \
	fi

clean:
	rm -rf bin dist

test-release-script:
	./scripts/release_test.sh