get:
	go get -t ./...

fmt: get
	go fmt ./...

vet: fmt
	go vet ./...

build: vet
	go build ./...

get-ginkgo:
	go get github.com/onsi/ginkgo/ginkgo

test: vet get-ginkgo
	ginkgo

test-docker:
	docker build -f Dockerfile.test .