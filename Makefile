VERSION ?= 0.1.0-dev
.PHONY: tools test integration e2e build

tools:
	tools/fetch.sh

test:
	cd core && go vet ./... && go test -count=1 ./...

integration:
	cd core && RELICAL_RESTIC=$(CURDIR)/.tools/restic RELICAL_REST_SERVER=$(CURDIR)/.tools/rest-server \
		go test -tags integration -count=1 ./...

e2e:
	core/tests/e2e/restore-dovecot.sh

build:
	cd core && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" \
		-o ../dist/relical ./cmd/relical
