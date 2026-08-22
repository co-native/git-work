BINARY := git-work
DIST_DIR := dist
INSTALL_DIR := $(HOME)/.local/bin

# Markdown is formatted with `deno fmt` at 90 columns. Note that deno fmt also
# reformats fenced yaml/json blocks, so a block whose columns are hand-aligned
# needs an <!-- deno-fmt-ignore --> above it.
MD_WIDTH := 90
MD_FILES := $(shell find . -name '*.md' -not -path './dist/*' -not -path './.git/*')

# CVSS floor for the vulnerability targets. Empty means `vulncheck` reports
# everything; the fix-dep-vulns targets require a floor and default to 7.0.
# Advisories with no published score are always listed regardless of this value,
# and are never auto-fixed; scripts/check-dependency-vulns.sh explains why.
MIN_SCORE ?=
FIX_MIN_SCORE := $(or $(MIN_SCORE),7.0)


.PHONY: build install clean test fmt fmt-check fmt-md fmt-md-check fmt-go fmt-go-check vulncheck fix-dep-vulns fix-dep-vulns-yes

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

# Reports known vulnerabilities across the whole module graph, not just the
# go.mod require list, and annotates each with whether govulncheck can reach
# it. Deliberately not wired into CI: it depends on three third-party APIs, so
# a network blip would fail builds for reasons unrelated to the change.
vulncheck:
	@./scripts/check-dependency-vulns.sh $(if $(MIN_SCORE),-m $(MIN_SCORE))

# Plans upgrades past every finding at or above FIX_MIN_SCORE and prints them.
# Changes nothing. Findings in modules absent from go.mod are skipped: they are
# not compiled in, so upgrading them would add a dependency this module does not
# use. Targets the lowest release with no known vulns, not merely the lowest one
# fixing the matched CVE, so a policy gate does not block again on the next.
fix-dep-vulns:
	@./scripts/check-dependency-vulns.sh --fix --min-score $(FIX_MIN_SCORE)

# Same, but applies the plan and then verifies: tidy, build, vet, rescan, tests.
fix-dep-vulns-yes:
	@./scripts/check-dependency-vulns.sh --fix --yes --min-score $(FIX_MIN_SCORE)
	$(MAKE) test
