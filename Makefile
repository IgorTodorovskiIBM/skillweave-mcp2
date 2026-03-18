.PHONY: build run fmt lint test

build:
	go build ./...

run:
	go run .

fmt:
	gofmt -w *.go

lint:
	go vet ./...

test:
	go test ./...
