Binary := og

.PHONY: build install test vet clean

build:
	go build -o $(Binary) ./cmd/og

install:
	go install ./cmd/og

test:
	go test ./...

vet:
	go vet ./...

clean:
	rm -f $(Binary)
