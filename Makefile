DQLITE_BUILD_SCRIPTS_DIR ?= $(shell pwd)/hack
GO_SOURCES = $(shell find . -name '*.go')
LOCALBIN ?= $(shell pwd)/bin
DOCS_OUTPUT_DIR ?= .docs
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

## Development
go.fmt:
	go mod tidy
	go fmt ./...

go.lint:
ifeq (, $(shell which golangci-lint))
	echo "golangci-lint not found, installing it"
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@v2.7.2
endif
	golangci-lint run

go.vet:
	$(DQLITE_BUILD_SCRIPTS_DIR)/static-go-vet.sh ./...

# Note: coverage.txt must remain raw and unfiltered because .github/workflows/tics.yaml consumes it.
go.unit:
	$(DQLITE_BUILD_SCRIPTS_DIR)/static-go-test.sh -v ./pkg/... ./cmd/... -covermode=atomic -coverprofile=coverage.txt

COVERAGE_BASE ?=
go.coverage:
	$(DQLITE_BUILD_SCRIPTS_DIR)/coverage.sh $(if $(COVERAGE_BASE),-b $(COVERAGE_BASE))

coverage.filtered.txt: coverage.txt $(DQLITE_BUILD_SCRIPTS_DIR)/coverage.conf $(DQLITE_BUILD_SCRIPTS_DIR)/coverage.sh
	COVERAGE_THRESHOLD=0 $(DQLITE_BUILD_SCRIPTS_DIR)/coverage.sh -o /dev/null

go.coverage.html: coverage.filtered.txt
	go tool cover -html=coverage.filtered.txt -o coverage.html

.PHONY: go.coverage go.coverage.html

go.doc: bin/static/k8s
	bin/static/k8s generate-docs --output-dir $(DOCS_OUTPUT_DIR) --project-dir .

## Static Builds
static: bin/static/k8s bin/static/k8sd bin/static/k8s-apiserver-proxy

$(DQLITE_BUILD_SCRIPTS_DIR)/.deps/static/lib/libdqlite.a:
	$(DQLITE_BUILD_SCRIPTS_DIR)/static-dqlite.sh

bin/static/k8s: $(DQLITE_BUILD_SCRIPTS_DIR)/.deps/static/lib/libdqlite.a $(GO_SOURCES)
	mkdir -p bin/static
	$(DQLITE_BUILD_SCRIPTS_DIR)/static-go-build.sh -o bin/static/k8s ./cmd/main.go

bin/static/k8s-apiserver-proxy: bin/static/k8s
	cd bin/static && ln -sf k8s k8s-apiserver-proxy

bin/static/k8sd: bin/static/k8s
	cd bin/static && ln -sf k8s k8sd

## Dynamic Builds
dynamic: bin/dynamic/k8s bin/dynamic/k8sd bin/dynamic/k8s-apiserver-proxy

bin/dynamic/lib/libdqlite.so:
	mkdir -p bin/dynamic/lib
	$(DQLITE_BUILD_SCRIPTS_DIR)/dynamic-dqlite.sh
	cp $(DQLITE_BUILD_SCRIPTS_DIR)/.deps/dynamic/lib/*.so* bin/dynamic/lib

bin/dynamic/k8s: bin/dynamic/lib/libdqlite.so
	mkdir -p bin/dynamic
	$(DQLITE_BUILD_SCRIPTS_DIR)/dynamic-go-build.sh -o bin/dynamic/k8s ./cmd/main.go

bin/dynamic/k8s-apiserver-proxy: bin/dynamic/k8s
	cd bin/dynamic && ln -s k8s k8s-apiserver-proxy

bin/dynamic/k8sd: bin/dynamic/k8s
	cd bin/dynamic && ln -s k8s k8sd

## Cleanup
clean:
	rm -rf bin hack/.build hack/.deps
