.PHONY: build test lint run compose-up compose-down

APP_NAME ?= bcrs-tracker
BIN_DIR ?= bin

build:
	CGO_ENABLED=0 go build -ldflags="-s -w" -o $(BIN_DIR)/$(APP_NAME) ./cmd/tracker

test:
	go test -race -v -coverprofile=coverage.out ./...

lint:
	go vet ./...

run:
	go run ./cmd/tracker

compose-up:
	docker compose up --build -d

compose-down:
	docker compose down
