.PHONY: fmt fmt-check lint test cover tidy-check vuln check

fmt:
	go tool gofumpt -w .

fmt-check:
	test -z "$$(go tool gofumpt -l .)"

lint:
	golangci-lint run

test:
	go test -race ./...

cover:
	go test -race -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

tidy-check:
	go mod tidy
	git diff --exit-code go.mod go.sum

vuln:
	go tool govulncheck ./...

check: fmt-check lint test tidy-check vuln
