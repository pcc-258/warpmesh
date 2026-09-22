.PHONY: web server agent build test build-linux build-agent-windows build-agent-darwin cross

web:
	cd web && npm install && npm run build

server:
	go build -o bin/devicerelay-server ./cmd/server

agent:
	go build -o bin/devicerelay-agent ./cmd/agent

build: web server agent

test:
	go test ./...

build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/devicerelay-server-linux-amd64 ./cmd/server
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/devicerelay-agent-linux-amd64 ./cmd/agent

build-agent-windows:
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o bin/devicerelay-agent-windows-amd64.exe ./cmd/agent

build-agent-darwin:
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o bin/devicerelay-agent-darwin-arm64 ./cmd/agent

cross: build-linux build-agent-windows build-agent-darwin
