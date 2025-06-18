GIT_COMMIT ?= $(shell git describe --dirty=-unsupported --always --tags || echo pre-commit)
IMAGE_NAME ?= hotload-integration-tests:$(GIT_COMMIT)

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

get-ginkgo:
	go get github.com/onsi/ginkgo/v2/ginkgo

test: vet get-ginkgo go-test

go-test:
	go test -race github.com/infobloxopen/hotload \
		github.com/infobloxopen/hotload/fsnotify \
		github.com/infobloxopen/hotload/internal \
		github.com/infobloxopen/hotload/metrics \
		github.com/infobloxopen/hotload/modtime


# test target which includes the no-diff fail condition
ci-test: fmt tidy no-diff test

test-docker:
	docker build -f Dockerfile.test .

.integ-test-image-$(GIT_COMMIT):
	docker build -f Dockerfile.integrationtest . -t $(IMAGE_NAME)

integ-test-image: .integ-test-image-$(GIT_COMMIT)

# this'll run outside of the build container
deploy-integration-tests:
	helm upgrade hotload-integration-tests integrationtests/helm/hotload-integration-tests -i --set image.tag=$(GIT_COMMIT)

build-test: vet get-ginkgo
	go test -c ./integrationtests

kind-create-cluster:
	kind create cluster

kind-load:
	kind load docker-image $(IMAGE_NAME)

ci-integration-tests: integ-test-image kind-load deploy-integration-tests
	(helm test --timeout=600s hotload-integration-tests || (kubectl logs hotload-integration-tests-job && exit 1)) && kubectl logs hotload-integration-tests-job

delete-all:
	helm uninstall hotload-integration-tests || true
	kubectl delete pvc --all || true
	kubectl delete pods --all || true

postgres-docker-compose-up:
	cd integrationtests/docker; docker compose up --detach

postgres-docker-compose-down:
	cd integrationtests/docker; docker compose down

# Requires postgres db, see target postgres-docker-compose-up
local-integration-tests:
	HOTLOAD_PATH_CHKSUM_METRICS_ENABLE=true go test -v -race -timeout=3m github.com/infobloxopen/hotload/integrationtests
