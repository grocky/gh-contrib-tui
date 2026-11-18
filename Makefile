.DEFAULT_GOAL := help

GO       := go
MODULE   := $(shell $(GO) list -m)
BIN      := bin
CMDS     := gh-contrib-tui screenshots ansi2svg
TARGETS  := $(addprefix $(BIN)/,$(CMDS))
SRC      := $(shell find . -type f -name '*.go' -not -path './vendor/*') go.mod go.sum

GOIMPORTS_VERSION   := v0.53.0
STATICCHECK_VERSION := v0.8.2
GORELEASER_VERSION  := v2.20.2

GOIMPORTS   := $(GO) run golang.org/x/tools/cmd/goimports@$(GOIMPORTS_VERSION) -local $(MODULE)
STATICCHECK := $(GO) run honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION)
GORELEASER  := $(GO) run github.com/goreleaser/goreleaser/v2@$(GORELEASER_VERSION)

Q = $(if $(filter 1,$V),,@)
M = $(shell printf "$(BLUE)▶$(RESET)")

.PHONY: help
help: ## print this help message
	@awk -F ':|##' '/^[^\t].+?:.*?##/ { printf "$(GREEN)%-20s$(RESET)%s\n", $$1, $$NF }' $(MAKEFILE_LIST)

# ==================================================================================== #
# BUILD
# ==================================================================================== #

.PHONY: build
build: $(TARGETS) ## build every command into bin/

$(BIN)/%: $(SRC) ; $(info $(M) building $@...)
	$Q $(GO) build -o $@ ./cmd/$*

.PHONY: run
run: ## run the TUI from source
	$Q $(GO) run ./cmd/gh-contrib-tui

.PHONY: screenshots
screenshots: ## regenerate docs/images/*.svg
	$Q $(GO) run ./cmd/screenshots

.PHONY: clean
clean: ; $(info $(M) cleaning...) @ ## remove build, release and coverage artifacts
	$Q rm -rf $(BIN) dist coverage.out

# ==================================================================================== #
# TEST
# ==================================================================================== #

.PHONY: test
test: ; $(info $(M) running tests...) @ ## run the test suite
	$Q $(GO) test ./...

.PHONY: test-race
test-race: ; $(info $(M) running tests with the race detector...) @ ## run the test suite with the race detector
	$Q $(GO) test -race ./...

.PHONY: cover
cover: ; $(info $(M) running tests with coverage...) @ ## run tests and open the coverage report
	$Q $(GO) test -coverprofile=coverage.out ./...
	$Q $(GO) tool cover -html=coverage.out

# ==================================================================================== #
# QUALITY CONTROL
# ==================================================================================== #

.PHONY: fmt
fmt: ; $(info $(M) formatting...) @ ## format and group imports in all Go source files (goimports)
	$Q $(GOIMPORTS) -w cmd internal

.PHONY: tidy
tidy: fmt ; $(info $(M) tidying modfile...) @ ## format code and tidy the modfile
	$Q $(GO) mod tidy -v

.PHONY: fmt-check
fmt-check: ; $(info $(M) checking formatting...) @ ## fail if any file is not goimports clean
	$Q out=$$($(GOIMPORTS) -l cmd internal); if [ -n "$$out" ]; then echo "not goimports clean:"; echo "$$out"; exit 1; fi

.PHONY: lint
lint: ; $(info $(M) linting...) @ ## run go vet and staticcheck
	$Q $(GO) vet ./...
	$Q $(STATICCHECK) -checks=all,-ST1000,-U1000 ./...

.PHONY: audit
audit: fmt-check lint test-race ; $(info $(M) verifying modules...) @ ## run every check (formatting, lint, race tests, module verification)
	$Q $(GO) mod verify

# ==================================================================================== #
# RELEASE
# ==================================================================================== #

.PHONY: release-check
release-check: ; $(info $(M) checking the release config...) @ ## validate .goreleaser.yaml
	$Q $(GORELEASER) check

.PHONY: snapshot
snapshot: ; $(info $(M) building a snapshot release...) @ ## build every release archive into dist/ without publishing
	$Q $(GORELEASER) release --snapshot --clean

# ==================================================================================== #
# HELPERS
# ==================================================================================== #

GREEN := $(shell tput -Txterm setaf 2)
BLUE  := $(shell tput -Txterm setaf 4)
RESET := $(shell tput -Txterm sgr0)
