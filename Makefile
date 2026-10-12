.PHONY: test vet fmt-check lint check build snapshot clean

test:
	go test -race ./...

vet:
	go vet ./...

fmt-check:
	@test -z "$$(gofmt -l .)" || { echo "gofmt needed on:"; gofmt -l .; exit 1; }

lint:
	go run honnef.co/go/tools/cmd/staticcheck@v0.8.1 ./...

check: test vet fmt-check lint

build:
	go build -o bin/claude-migrate ./cmd/claude-migrate

# Local GoReleaser build; publishes nothing.
snapshot:
	go run github.com/goreleaser/goreleaser/v2@v2.18.3 build --snapshot --clean

clean:
	rm -rf bin dist
