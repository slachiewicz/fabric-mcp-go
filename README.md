# fabric-mcp-go

A Go implementation of the [Microsoft Fabric MCP Server](https://github.com/microsoft/mcp/tree/main/servers/Fabric.Mcp.Server).
It keeps the upstream tool names, parameters and result format, so an existing `mcp.json` entry can point at this binary instead.

Status: work in progress. The `docs_*` and `core_*` tools are done; Data Factory and OneLake tools follow. Fabric API tools sign in with the Azure default credential chain (for example `az login`), falling back to a browser sign-in.

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

The target is upstream `main`, not the latest npm release.
`internal/parity` compares this server's `tools/list` with a snapshot of upstream's in `testdata/ref-tools.json`.
To also compare tool calls, build upstream with the .NET 10 SDK and point `FABMCP_REF` at the binary:

```bash
git clone --depth 1 https://github.com/microsoft/mcp.git
dotnet build mcp/servers/Fabric.Mcp.Server/src/Fabric.Mcp.Server.csproj -c Release
DOTNET_ROOT=~/.dotnet FABMCP_REF=$PWD/mcp/servers/Fabric.Mcp.Server/src/bin/Release/fabmcp \
  go test ./internal/parity/ -v
```

## License

MIT. Portions derived from Microsoft's Fabric MCP Server and the Fabric REST API specifications; see [NOTICE](NOTICE).
