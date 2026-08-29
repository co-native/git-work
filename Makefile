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

.DEFAULT_GOAL := help

.PHONY: help build install clean test fmt fmt-check fmt-md fmt-md-check fmt-go fmt-go-check vulncheck fix-dep-vulns fix-dep-vulns-yes

# A target's `## text` is its line in `make help`; targets without one are
# omitted, so a new target should carry one.
help: ## list the targets (the default)
	@grep -hE '^[a-zA-Z0-9_-]+:.*## ' $(MAKEFILE_LIST) | awk -F ':.*## ' '{ printf "  %-18s %s\n", $$1, $$2 }'
	@echo
	@echo "  MIN_SCORE=<cvss>   floor for vulncheck (default: all) and fix-dep-vulns (default: $(FIX_MIN_SCORE))"

build: ## build the binary into DIST_DIR
	go build -o $(DIST_DIR)/$(BINARY) .

# Installs as `git-work` on PATH so git dispatches `git work <cmd>`; make sure
# $(INSTALL_DIR) is on PATH. `go install github.com/co-native/git-work@latest`
# does the same thing without a checkout.
install: build ## build, then install the binary into INSTALL_DIR
	mkdir -p $(INSTALL_DIR)
	install -m 755 $(DIST_DIR)/$(BINARY) $(INSTALL_DIR)/$(BINARY)

clean: ## remove DIST_DIR
	rm -rf $(DIST_DIR)

test: ## go test ./...
	go test ./...

fmt: fmt-md fmt-go ## format markdown (deno fmt) and Go (gofmt)

fmt-check: fmt-md-check fmt-go-check ## fail if any file is not formatted

fmt-md: $(MD_FILES) ## format markdown only
	@command -v deno >/dev/null || { echo "deno not found (brew install deno)" >&2; exit 1; }
	@deno fmt --line-width=$(MD_WIDTH) $(MD_FILES)

fmt-md-check: $(MD_FILES) ## check markdown formatting only
	@command -v deno >/dev/null || { echo "deno not found (brew install deno)" >&2; exit 1; }
	@deno fmt --check --line-width=$(MD_WIDTH) $(MD_FILES)

# gofmt rather than `go fmt ./...`: only gofmt has a non-writing mode, and one
# tool for both halves keeps them honest about what "formatted" means.
fmt-go: ## format Go only
	@gofmt -l -w .

fmt-go-check: ## check Go formatting only
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
vulncheck: ## report known vulns across the module graph, with reachability
	@./scripts/check-dependency-vulns.sh $(if $(MIN_SCORE),-m $(MIN_SCORE))

# Plans upgrades past every finding at or above FIX_MIN_SCORE and prints them.
# Changes nothing. Findings in modules absent from go.mod are skipped: they are
# not compiled in, so upgrading them would add a dependency this module does not
# use. Targets the lowest release with no known vulns, not merely the lowest one
# fixing the matched CVE, so a policy gate does not block again on the next.
fix-dep-vulns: ## plan upgrades past findings at or above the floor; changes nothing
	@./scripts/check-dependency-vulns.sh --fix --min-score $(FIX_MIN_SCORE)

# Same, but applies the plan and then verifies: tidy, build, vet, rescan, tests.
fix-dep-vulns-yes: ## apply that plan, then tidy, build, vet, rescan and test
	@./scripts/check-dependency-vulns.sh --fix --yes --min-score $(FIX_MIN_SCORE)
	$(MAKE) test
