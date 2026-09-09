BINARY := rxs
PKG := ./cmd/rxs
TOOLS_DIR := $(CURDIR)/.tools
STATICCHECK_VERSION := v0.8.1
OSV_SCANNER_VERSION := v2.5.1
GOSEC_VERSION := v2.29.0
BENCHSTAT_VERSION := v0.0.0-20260908200009-22c9c6c9d4da
STATICCHECK := $(TOOLS_DIR)/staticcheck-$(STATICCHECK_VERSION)/staticcheck
OSV_SCANNER := $(TOOLS_DIR)/osv-scanner-$(OSV_SCANNER_VERSION)/osv-scanner
GOSEC := $(TOOLS_DIR)/gosec-$(GOSEC_VERSION)/gosec
BENCHSTAT := $(TOOLS_DIR)/benchstat-$(BENCHSTAT_VERSION)/benchstat

.PHONY: all build license-check licenses test vet race fuzz bench checks staticcheck osv-scanner gosec install-tools clean

all: build

build:
	go build -o $(BINARY) $(PKG)

license-check:
	./scripts/check-licenses.sh

licenses: license-check build
	./scripts/gen-licenses.sh $(BINARY) THIRD_PARTY_LICENSES.txt

test:
	go test ./...

vet:
	go vet ./...

race:
	go test -race -count=1 ./...

fuzz:
	go test ./internal/render -run '^$$' -fuzz '^FuzzLaTeX$$' -fuzztime=10s -parallel=2
	go test ./internal/render -run '^$$' -fuzz '^FuzzHTML$$' -fuzztime=10s -parallel=2
	go test ./internal/render -run '^$$' -fuzz '^FuzzMathLinkAnnotations$$' -fuzztime=10s -parallel=2
	go test ./internal/article -run '^$$' -fuzz '^FuzzArticlePreflight$$' -fuzztime=10s -parallel=2
	go test ./internal/opml -run '^$$' -fuzz '^FuzzImport$$' -fuzztime=10s -parallel=2

bench:
	go test ./internal/app ./internal/render ./internal/store ./internal/feed ./internal/article -run '^$$' -bench . -benchmem -count=10

checks: license-check vet race staticcheck osv-scanner gosec

staticcheck:
	"$(STATICCHECK)" ./...

osv-scanner:
	"$(OSV_SCANNER)" scan source -r .

gosec:
	"$(GOSEC)" ./...

install-tools:
	mkdir -p "$(dir $(STATICCHECK))" "$(dir $(OSV_SCANNER))" "$(dir $(GOSEC))" "$(dir $(BENCHSTAT))"
	GOBIN="$(dir $(STATICCHECK))" go install honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION)
	GOBIN="$(dir $(OSV_SCANNER))" go install github.com/google/osv-scanner/v2/cmd/osv-scanner@$(OSV_SCANNER_VERSION)
	GOBIN="$(dir $(GOSEC))" go install github.com/securego/gosec/v2/cmd/gosec@$(GOSEC_VERSION)
	GOBIN="$(dir $(BENCHSTAT))" go install golang.org/x/perf/cmd/benchstat@$(BENCHSTAT_VERSION)

clean:
	rm -f $(BINARY) THIRD_PARTY_LICENSES.txt
	go clean -testcache
