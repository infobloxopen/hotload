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
	ginkgo

# test target which includes the no-diff fail condition
ci-test: fmt no-diff test

test-docker:
	docker build -f Dockerfile.test .
