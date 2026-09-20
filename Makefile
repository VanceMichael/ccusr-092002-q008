
.PHONY: migrate test run
DATABASE_PATH ?= data/app.sqlite3
migrate:
	mkdir -p $$(dirname "$(DATABASE_PATH)")
	DATABASE_PATH="$(DATABASE_PATH)" go run ./cmd/migrate
test:
	go test ./...
run:
	go run ./cmd/server
