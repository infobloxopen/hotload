.PHONY: all get fmt tidy no-diff vet build test unit-test integration-test ci-test clean check-deps

get:
	go get -t ./...

fmt: get
	go fmt ./...

tidy:
	go mod tidy

# assert that there is no difference after running format
no-diff:
	git diff --exit-code

vet: fmt
	go vet ./...

build: vet
	go build ./...

# Run unit tests only (fast, no Docker required)
unit-test: vet
	go test -v -short -race ./...

# Run integration tests (requires Docker)
integration-test:
	cd test/integration && go test -v -race -timeout=5m

# Run all tests
test: unit-test integration-test

# CI test target with formatting and diff checks
ci-test: fmt tidy no-diff check-deps unit-test

# Check that only expected dependencies are present
check-deps:
	@echo "Checking go.mod dependencies..."
	@DIRECT_DEPS=$$(go list -json -m all | jq -s '.[1:] | map(select(.Indirect != true)) | .[].Path' -r); \
	EXPECTED="github.com/fsnotify/fsnotify"; \
	if [ "$$DIRECT_DEPS" != "$$EXPECTED" ]; then \
		echo "❌ Unexpected direct dependencies found!"; \
		echo "Expected: $$EXPECTED"; \
		echo "Found:    $$DIRECT_DEPS"; \
		exit 1; \
	fi; \
	echo "✅ Dependencies check passed: only expected dependencies present"

clean:
	go clean -testcache
	rm -f .integ-test-image-*
