BINARY := git-work
DIST_DIR := dist
INSTALL_DIR := $(HOME)/.local/bin

# Markdown is formatted with `deno fmt` at 90 columns. Note that deno fmt also
# reformats fenced yaml/json blocks, so a block whose columns are hand-aligned
# needs an <!-- deno-fmt-ignore --> above it.
MD_WIDTH := 90
MD_FILES := $(shell find . -name '*.md' -not -path './dist/*' -not -path './.git/*')

.PHONY: build install clean test fmt fmt-check fmt-md fmt-md-check fmt-go fmt-go-check

build:
	go build -o $(DIST_DIR)/$(BINARY) .

# Installs as `git-work` on PATH so git dispatches `git work <cmd>`; make sure
# $(INSTALL_DIR) is on PATH. `go install github.com/co-native/git-work@latest`
# does the same thing without a checkout.
install: build
	mkdir -p $(INSTALL_DIR)
	install -m 755 $(DIST_DIR)/$(BINARY) $(INSTALL_DIR)/$(BINARY)

clean:
	rm -rf $(DIST_DIR)

test:
	go test ./...

fmt: fmt-md fmt-go

fmt-check: fmt-md-check fmt-go-check

fmt-md: $(MD_FILES)
	@command -v deno >/dev/null || { echo "deno not found (brew install deno)" >&2; exit 1; }
	@deno fmt --line-width=$(MD_WIDTH) $(MD_FILES)

fmt-md-check: $(MD_FILES)
	@command -v deno >/dev/null || { echo "deno not found (brew install deno)" >&2; exit 1; }
	@deno fmt --check --line-width=$(MD_WIDTH) $(MD_FILES)

# gofmt rather than `go fmt ./...`: only gofmt has a non-writing mode, and one
# tool for both halves keeps them honest about what "formatted" means.
fmt-go:
	@gofmt -l -w .

fmt-go-check:
	@stale=$$(gofmt -l .); \
	if [ -n "$$stale" ]; then \
		echo "not gofmt-clean (run 'make fmt-go'):" >&2; \
		echo "$$stale" >&2; \
		exit 1; \
	fi
