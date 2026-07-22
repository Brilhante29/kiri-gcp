.PHONY: build run test vet docker clean

BINARY := kiri
PKG := ./cmd/kiri

build:
	go build -o $(BINARY) $(PKG)

run:
	go run $(PKG)

test:
	go test ./...

vet:
	go vet ./...

docker:
	docker build -t kiri -f docker/Dockerfile .

clean:
	rm -f $(BINARY)
