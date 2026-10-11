.PHONY: build test vet verify
build:
	go build -trimpath -o bin/breakmock ./cmd/breakmock
test:
	go test -race ./...
vet:
	go vet ./...
verify:
	python3 scripts/verify.py --jar $(WIREMOCK_JAR)
