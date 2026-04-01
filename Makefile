BINARY_NAME := ipv6-reachability-test

.PHONY: build-darwin-amd64
build-darwin-amd64:
	GOOS=darwin GOARCH=amd64 go build -o $(BINARY_NAME)-darwin-amd64 .

.PHONY: build-linux-amd64
build-linux-amd64:
	GOOS=linux GOARCH=amd64 go build -o $(BINARY_NAME)-linux-amd64 .

.PHONY: build-all
build-all: build-darwin-amd64 build-linux-amd64

.PHONY: clean
clean:
	rm -f $(BINARY_NAME)-darwin-amd64 $(BINARY_NAME)-linux-amd64
