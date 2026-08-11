.PHONY: run build tidy vet test clean dev

dev: run

run:
	go run ./cmd/server

build:
	go build -o bin/server ./cmd/server

tidy:
	go mod tidy

vet:
	go vet ./...

test:
	go test ./... -race -cover

clean:
	rm -rf bin/ data/
