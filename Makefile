.PHONY: build test vet fmt fmt-check staticcheck lint check tidy up down

GOFILES := $(shell find . -name '*.go' -not -path './vendor/*')

build:
	go build ./...

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -l -w $(GOFILES)

fmt-check:
	@test -z "$$(gofmt -l $(GOFILES))" || (echo "gofmt: the following files need formatting:"; gofmt -l $(GOFILES); exit 1)

staticcheck:
	staticcheck ./...

# Runs everything CI runs, in the same order, so a local pass predicts a CI pass.
check: fmt-check vet staticcheck test

tidy:
	go mod tidy

up:
	docker compose up -d

down:
	docker compose down
