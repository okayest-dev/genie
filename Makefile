Binary := genie

ProtoPluginsDir := prototype/og-cbu.5/plugins
ProtoPluginName := logging-plugin-proto

.PHONY: build install test vet clean proto-logging-plugin proto-plugin-test proto-run

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

# Build the og-cbu.5 logging plugin into the prototype plugins dir (nested module).
proto-logging-plugin:
	mkdir -p prototype/og-cbu.5/plugins/$(ProtoPluginName)
	cd prototype/og-cbu.5/logging-plugin && go build -o ../plugins/$(ProtoPluginName)/$(ProtoPluginName) .
	cp prototype/og-cbu.5/logging-plugin/manifest.toml prototype/og-cbu.5/plugins/$(ProtoPluginName)/manifest.toml

# Run the seam tests that drive the real, built plugin end to end.
proto-plugin-test:
	go test ./internal/plugin/ -run 'TestLifecycleSeam|TestContextSeam' -v
	go test ./internal/agent/ -run 'TestLoggingPlugin' -v
