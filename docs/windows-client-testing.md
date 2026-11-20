# Windows Client Testing

The client has two test surfaces: the WebView2 host and the web application it
renders. They need different evidence.

## Automated Gates

Run the repository gates before packaging:

```bash
GOCACHE=/tmp/warpmesh-gocache go test ./...
GOCACHE=/tmp/warpmesh-gocache golangci-lint run ./...
cd web
npm test
npx eslint .
npx tsc --noEmit
npm run build
```

The Go suite covers the automation API, MCP protocol, link lifecycle, local UI
contracts and the native server/node process smoke test. CI additionally builds
`warpmesh-client.exe` on `windows-2025`.

## Browser-Layer Checks

Start a node with a test data directory and open `http://127.0.0.1:9876`.
Playwright or the in-app browser can exercise the same DOM that WebView2 loads:

- Overview renders connected, reconnecting and desktop-unavailable states.
- Links can be created, updated, copied and deleted; active counters update.
- Peers show the same local and target port mapping.
- Access pages expose terminal, desktop and authorization actions.
- Automation lists all eight MCP tools and loads capabilities without a token.
- `GET /api/v1/ai-guide` renders or downloads the embedded operating guide.
- The page remains readable at desktop and narrow viewports without clipped
  names or overlapping controls.

## Native Windows Checks

Run these on a Windows VM or physical host. They cannot be inferred from a
cross-build.

1. Launch `warpmesh-client.exe` with WebView2 installed and confirm the native
   window loads the node UI.
2. Repeat without WebView2 and confirm the default-browser fallback.
3. Stop the node service before launch and confirm the unavailable screen
   appears, then start the service and confirm the client recovers.
4. Verify normal close leaves `warpmesh-node` running.
5. Check 100%, 150% and 200% display scaling plus a 1366x768 display.
6. Exercise terminal, desktop and file access through an authorized operator
   account; repeat with an ungranted operator and confirm server rejection.
7. Run as a standard user and as the installed service identity to catch path
   or token-permission problems.

## API And AI Checks

The automation API must reject requests without the bearer token and reject
DNS-rebinding Host headers. MCP tests should verify tool discovery, structured
results, the `warpmesh://node/guide` Markdown resource, read-only snapshot
resources and both success and failure results.

An end-to-end MCP smoke test can create and delete a link:

```json
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}
{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"node_create_link","arguments":{"localPort":2222,"targetDeviceId":"dev-example","targetPort":22}}}
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"node_delete_link","arguments":{"localPort":2222}}}
```

Pass these lines to `warpmesh-node mcp -data-dir <dir>`.
