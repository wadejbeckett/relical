VERSION ?= 0.2.0
RELICAL_RESTIC ?= $(CURDIR)/.tools/restic
RELICAL_REST_SERVER ?= $(CURDIR)/.tools/rest-server
.PHONY: tools test integration e2e build plesk-ext

tools:
	tools/fetch.sh

test:
	cd core && go vet ./... && go test -count=1 ./...

integration:
	cd core && RELICAL_RESTIC=$(RELICAL_RESTIC) RELICAL_REST_SERVER=$(RELICAL_REST_SERVER) \
		go test -tags integration -count=1 ./...

e2e:
	core/tests/e2e/restore-dovecot.sh

build:
	cd core && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" \
		-o ../dist/relical ./cmd/relical

# The Plesk extension package (dist/relical-$(VERSION).zip). build-panel.sh
# builds the core and the extension from `git archive` of one commit
# (BUILD_REF, default HEAD), never from the working tree: commit first.
plesk-ext:
	build/build-panel.sh plesk $(VERSION)
