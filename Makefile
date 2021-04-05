GIT_COMMIT ?= $(shell git describe --dirty=-unsupported --always --tags || echo pre-commit)
IMAGE_NAME ?= hotload-integration-tests:$(GIT_COMMIT)
KIND_CLUSTER_NAME = hotload-test

get:
	go get -t ./...

fmt: get
	go fmt ./...

# assert that there is no difference after running format
no-diff:
	git diff --exit-code

vet: fmt
	go vet ./...

build: vet
	go build ./...

get-ginkgo:
	go get github.com/onsi/ginkgo/ginkgo

test: vet get-ginkgo
	go test -race ./...

# test target which includes the no-diff fail condition
ci-test: fmt no-diff test

test-docker:
	docker build -f Dockerfile.test .

.integ-test-image-$(GIT_COMMIT):
	docker build -f Dockerfile.integrationtest . -t $(IMAGE_NAME)

integ-test-image: .integ-test-image-$(GIT_COMMIT)

# this'll run outside of the build container
deploy-integration-tests:
	helm install hotload-integration-tests integrationtests/helm/hotload-integration-tests  --set image.tag=$(GIT_COMMIT)

build-test: vet get-ginkgo
	go test -c ./integrationtests

kind-create-cluster:
	kind create cluster --name $(KIND_CLUSTER_NAME)

kind-load:
	kind load docker-image --name $(KIND_CLUSTER_NAME) $(IMAGE_NAME)


ci-integration-tests: integ-test-image kind-load deploy-integration-tests
	# kubectl wait on condition or timeout