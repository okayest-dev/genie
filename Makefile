Binary := genie

.PHONY: build install test vet clean

build:
	go build -o $(Binary) ./cmd/genie

install:
	go install ./cmd/genie

test:
	go test ./...

vet:
	go vet ./...

clean:
	rm -f $(Binary)
