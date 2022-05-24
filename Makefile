GITCOMMIT := $(shell git rev-parse --short HEAD 2>/dev/null)
GIT_TAG := $(shell git describe --tags 2>/dev/null)

LDFLAGS := -s -w -X github.com/tomcz/openldap_exporter.commit=${GITCOMMIT}
LDFLAGS := ${LDFLAGS} -X github.com/tomcz/openldap_exporter.tag=${GIT_TAG}
OUTFILE ?= openldap_exporter

.PHONY: precommit
precommit: clean format lint compile

.PHONY: commit
commit: clean cross-compile
	ls -lha target/

.PHONY: clean
clean:
	rm -rf target

target:
	mkdir target

.PHONY: format
format:
ifeq (, $(shell which goimports))
	go install golang.org/x/tools/cmd/goimports@latest
endif
	@echo "Running goimports ..."
	@goimports -w -local github.com/tomcz/openldap_exporter $(shell find . -type f -name '*.go' | grep -v '/vendor/')

.PHONY: lint
lint:
ifeq (, $(shell which staticcheck))
	go install honnef.co/go/tools/cmd/staticcheck@latest
endif
	@echo "Running staticcheck ..."
	@staticcheck $(shell go list ./... | grep -v /vendor/)

.PHONY: compile
compile: target
	go build -ldflags "${LDFLAGS}" -o target/${OUTFILE} ./cmd/openldap_exporter/...
	gzip -c < target/${OUTFILE} > target/${OUTFILE}.gz

.PHONY: cross-compile
cross-compile:
	OUTFILE=openldap_exporter-linux-amd64 GOOS=linux GOARCH=amd64 $(MAKE) compile
	OUTFILE=openldap_exporter-linux-nocgo CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(MAKE) compile
	OUTFILE=openldap_exporter-osx-amd64 GOOS=darwin GOARCH=amd64 $(MAKE) compile
	OUTFILE=openldap_exporter-osx-arm64 GOOS=darwin GOARCH=arm64 $(MAKE) compile

.PHONY: image
image:
	docker build -t openldap-exporter:1.0.0 --platform=linux/amd64 .
