# fabric-mcp-go

A Go implementation of the [Microsoft Fabric MCP Server](https://github.com/microsoft/mcp/tree/main/servers/Fabric.Mcp.Server).
It keeps the upstream tool names, parameters and result format, so an existing `mcp.json` entry can point at this binary instead.

Status: work in progress. Only the `docs_*` tools are being ported so far; OneLake, core and Data Factory tools follow.

## Build and run

```bash
go build -o fabmcp ./cmd/fabmcp
./fabmcp server start
```

To register the server in Claude Code:

```bash
claude mcp add fabric -- /path/to/fabmcp server start
```

## Parity with upstream

`internal/parity` compares this server's `tools/list` with a snapshot from the upstream release in `testdata/ref-tools.json`.
To compare tool calls against the live upstream binary, set `FABMCP_REF` to its path:

```bash
FABMCP_REF=/path/to/upstream/fabmcp go test ./internal/parity/ -v
```

## License

MIT. Portions derived from Microsoft's Fabric MCP Server and the Fabric REST API specifications; see [NOTICE](NOTICE).
