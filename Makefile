.PHONY: web server agent build test lint-go lint-web checksmell build-linux build-agent-windows build-agent-darwin cross

web:
	cd web && npm install && npm run build

server:
	go build -o bin/warpmesh-server ./cmd/server

agent:
	go build -o bin/warpmesh-agent ./cmd/agent

build: web server agent

test:
	go test ./...

lint-go:
	golangci-lint run ./...

lint-web:
	cd web && npx eslint .

checksmell: lint-go lint-web

build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/warpmesh-server-linux-amd64 ./cmd/server
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/warpmesh-agent-linux-amd64 ./cmd/agent

build-agent-windows:
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o bin/warpmesh-agent-windows-amd64.exe ./cmd/agent

build-agent-darwin:
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o bin/warpmesh-agent-darwin-arm64 ./cmd/agent

cross: build-linux build-agent-windows build-agent-darwin
