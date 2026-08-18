.PHONY: test lint install

install:
	go mod download

test:
	go test ./...

lint:
	golangci-lint run
