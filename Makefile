# Development shortcuts. The deploy scripts live in deploy/.

BINARY      ?= tbproxy
IMAGE       ?= ticketbutler-proxy:local
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

# Path to the real TicketButler payload, which is never committed. Override when
# your checkout of the automation repo sits somewhere else:
#   make fixture REAL_ORDERS=~/Downloads/orders.json
REAL_ORDERS ?= ../automation/appscript/orders.json

.DEFAULT_GOAL := help
.PHONY: help test lint build run fixture parity docker

help: ## List the available targets
	@awk -F ":.*## " "/^[a-z-]+:.*## /{printf \"  %-10s %s\\n\", \$$1, \$$2}" $(MAKEFILE_LIST)

test: ## Run the unit tests
	go test ./...

lint: ## Vet, then golangci-lint if it is installed
	go vet ./...
	@command -v golangci-lint >/dev/null 2>&1 \
		&& golangci-lint run \
		|| echo "golangci-lint not installed, skipping (CI runs it)"

build: ## Build the tbproxy binary for this machine
	go build -trimpath -ldflags="-X main.version=$(VERSION)" -o $(BINARY) ./cmd/tbproxy

run: ## Serve on :8080 from the anonymized fixture, no upstream and no bucket
	TICKETBUTLER_FILE=testdata/orders.sample.json \
	TICKETBUTLER_EVENT_UUID=00000000-0000-0000-0000-000000000000 \
	API_TOKENS=dev-token-for-local-only \
	SPONSOR_TICKET_TYPE_PKS=183067 \
	go run ./cmd/tbproxy serve

fixture: ## Regenerate testdata/orders.sample.json from REAL_ORDERS and verify it
	@test -f "$(REAL_ORDERS)" || { echo "no payload at $(REAL_ORDERS); set REAL_ORDERS to its path"; exit 1; }
	@# Via a temporary file: a failing anonymize.py with the redirect applied
	@# directly would truncate the committed fixture.
	python3 testdata/anonymize.py "$(REAL_ORDERS)" > testdata/orders.sample.json.new
	mv testdata/orders.sample.json.new testdata/orders.sample.json
	python3 testdata/verify_fixture.py "$(REAL_ORDERS)" testdata/orders.sample.json

# ORDERS defaults to the committed fixture so this runs anywhere. Point it at a real
# payload before a release: the fixture's anonymized company names collapse several
# sponsors onto one, so the sponsor comparison only means something on real data.
ORDERS ?= testdata/orders.sample.json

parity: ## Check the Go aggregation still agrees with the original Apps Script
	@command -v node >/dev/null 2>&1 || { echo "parity needs node"; exit 1; }
	@node testdata/parity/legacy.js "$(ORDERS)" > $(TMPDIR)tbproxy-legacy.json
	@go run ./cmd/tbproxy verify -file "$(ORDERS)" -sponsor-pks 183067 \
		2>/dev/null > $(TMPDIR)tbproxy-go.json
	@python3 testdata/parity/parity.py $(TMPDIR)tbproxy-legacy.json $(TMPDIR)tbproxy-go.json "$(ORDERS)"

docker: ## Build the container image for linux/amd64, the only thing Cloud Run runs
	docker buildx build --platform linux/amd64 --build-arg VERSION=$(VERSION) -t $(IMAGE) .
