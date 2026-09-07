Binary := genie

.PHONY: build install test vet clean gen-protocol

build:
	go build -o $(Binary) ./cmd/genie

install:
	go install ./cmd/genie

test:
	go test ./...

vet:
	go vet ./...

# Regenerate the plugin-facing wireplugin package from protocol/schema.yaml.
# Standalone plugin repos vendor this output as internal/wireplugin/.
gen-protocol:
	go run ./protocol -schema protocol/schema.yaml -out protocol/wireplugin

clean:
	rm -f $(Binary)
