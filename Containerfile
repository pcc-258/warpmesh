FROM alpine:3.20

RUN apk add --no-cache ca-certificates

ARG AGENT_BINARY=bin/warpmesh-agent-linux-amd64

COPY ${AGENT_BINARY} /usr/local/bin/warpmesh-agent

ENTRYPOINT ["/usr/local/bin/warpmesh-agent"]
