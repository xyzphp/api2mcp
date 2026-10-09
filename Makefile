.PHONY: init run mock test build up down logs dev smoke

init:
	go run ./cmd/setup
run:
	go run ./cmd/api2mcp
mock:
	go run ./cmd/mockapi
test:
	go test -race ./...
	npm test
build:
	go build -trimpath -o bin/api2mcp ./cmd/api2mcp
	go build -trimpath -o bin/mockapi ./cmd/mockapi
up:
	./scripts/compose.sh up --build -d
down:
	./scripts/compose.sh down
logs:
	./scripts/compose.sh logs -f api2mcp mock-api
dev:
	go run ./cmd/dev
smoke:
	go run ./cmd/smoke
