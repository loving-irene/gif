.PHONY: test build

test:
	go test ./...
	go vet ./...

build:
	go build -buildvcs=false -trimpath -o gif-server ./cmd/server
