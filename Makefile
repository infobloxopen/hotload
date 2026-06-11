# The repository holds four Go modules: the hotload core (.), the
# prometheus adapter (observability), the Kubernetes Secret strategy
# (k8ssecret), and the postgres integration tests (test/integration). The
# committed go.work ties them together for development; most targets loop
# over all of them.
MODULES := . k8ssecret observability test/integration

.PHONY: fmt vet tidy build test generate no-diff dep-budget ci-test \
	postgres-docker-compose-up postgres-docker-compose-down local-integration-tests

fmt:
	@for m in $(MODULES); do (cd $$m && go fmt ./...) || exit 1; done

vet:
	@for m in $(MODULES); do (cd $$m && go vet ./...) || exit 1; done

tidy:
	@for m in $(MODULES); do (cd $$m && go mod tidy) || exit 1; done

build:
	@for m in $(MODULES); do (cd $$m && go build ./...) || exit 1; done

# Unit tests. The integration module skips itself when postgres is not
# reachable; use local-integration-tests to run it for real.
test:
	@for m in $(MODULES); do (cd $$m && go test -race -timeout=5m -count=1 ./...) || exit 1; done

# Regenerate the optional-interface combination wrappers (conn/stmt) and the
# dbfake capability views.
generate:
	go generate ./...

# assert that there is no difference after running format/tidy/generate
no-diff:
	git diff --exit-code

# The hotload core must stay near-stdlib-only: its sole direct dependency is
# fsnotify. Fails when dependency creep adds more.
dep-budget:
	@reqs=$$(go mod edit -json | go run ./internal/depbudget); \
	if [ "$$reqs" != "github.com/fsnotify/fsnotify" ]; then \
		echo "dependency budget exceeded; direct requires of the root module:"; \
		echo "$$reqs"; \
		exit 1; \
	fi

ci-test: fmt tidy generate no-diff vet test dep-budget

postgres-docker-compose-up:
	cd test/integration/docker; docker compose up --detach --wait

postgres-docker-compose-down:
	cd test/integration/docker; docker compose down

# Requires postgres, see target postgres-docker-compose-up
local-integration-tests:
	cd test/integration && \
		HOTLOAD_INTEGRATION_TESTS=1 HOTLOAD_PATH_CHKSUM_METRICS_ENABLE=true \
		go test -v -race -timeout=5m -count=1 ./...
