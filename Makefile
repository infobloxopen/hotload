get:
	go get -t ./...

fmt: get
	go fmt ./...

vet: fmt
	go vet ./...

build: vet
	go build ./...

test: vet
	go test -v ./...

