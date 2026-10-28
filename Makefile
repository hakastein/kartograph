.PHONY: go ladybug build

go: ladybug
	test -z "$$(gofmt -l .)" || { gofmt -l .; exit 1; }
	go vet ./...
	go test -race ./...

ladybug:
	internal/ladybug/fetch.sh

build: ladybug
	go build -o bin/kartograph ./cmd/kartograph
