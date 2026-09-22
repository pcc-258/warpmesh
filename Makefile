.PHONY: web server agent build test

web:
	cd web && npm install && npm run build

server:
	go build -o bin/devicerelay-server ./cmd/server

agent:
	go build -o bin/devicerelay-agent ./cmd/agent

build: web server agent

test:
	go test ./...
