FROM alpine:3.20

RUN apk add --no-cache ca-certificates

ARG NODE_BINARY=bin/warpmesh-node-linux-amd64

COPY ${NODE_BINARY} /usr/local/bin/warpmesh-node

ENTRYPOINT ["/usr/local/bin/warpmesh-node"]
