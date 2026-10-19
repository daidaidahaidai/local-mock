.PHONY: build test vet fmt
build:
	mkdir -p bin
	go build -trimpath -o bin/localmock ./cmd/localmock
test:
	go test -race ./...
vet:
	go vet ./...
fmt:
	gofmt -w cmd internal examples/http-smoke/app
