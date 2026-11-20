.PHONY: web server node client-windows build test test-race test-web typecheck lint-go lint-web checksmell build-linux build-node-windows build-node-darwin cross verify

web:
	cd web && npm ci && npm run build

server:
	go build -ldflags="$(BUILD_INFO)" -o bin/warpmesh-server ./cmd/server

node:
	go build -ldflags="$(BUILD_INFO)" -o bin/warpmesh-node ./cmd/node

BUILD_INFO := $(shell bash .github/scripts/build-info.sh)

client-windows:
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="-H windowsgui $(BUILD_INFO)" -o bin/warpmesh-client.exe ./cmd/windowsclient

build: web server node

test:
	go test ./...

# The relay is heavily concurrent; run this before touching session or relay code.
test-race:
	go test -race ./...

test-web:
	cd web && npm test

typecheck:
	cd web && npx tsc --noEmit

lint-go:
	golangci-lint run ./...

lint-web:
	cd web && npx eslint .

checksmell: lint-go lint-web

# Full pre-PR gate required by the repository instructions.
verify: test test-race test-web lint-go lint-web typecheck web

build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="$(BUILD_INFO)" -o bin/warpmesh-server-linux-amd64 ./cmd/server
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="$(BUILD_INFO)" -o bin/warpmesh-node-linux-amd64 ./cmd/node

build-node-windows:
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags="$(BUILD_INFO)" -o bin/warpmesh-node-windows-amd64.exe ./cmd/node
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="-H windowsgui $(BUILD_INFO)" -o bin/warpmesh-client.exe ./cmd/windowsclient

build-node-darwin:
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags="$(BUILD_INFO)" -o bin/warpmesh-node-darwin-arm64 ./cmd/node

cross: build-linux build-node-windows build-node-darwin
