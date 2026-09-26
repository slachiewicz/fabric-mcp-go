# fabric-mcp-go

A Go implementation of the [Microsoft Fabric MCP Server](https://github.com/microsoft/mcp/tree/main/servers/Fabric.Mcp.Server).
It exposes the same 48 tools with the same names, parameters and result format as upstream `main`, so an existing `mcp.json` entry can point at this binary instead.
It ships as a single static binary with no .NET runtime.

The tools are grouped in four areas:

| Area | Tools | What they do |
|---|---|---|
| `docs` | 6 | Fabric OpenAPI specs, item definitions, best practices and API examples, embedded in the binary. No sign-in needed. |
| `core` | 2 | Search the OneLake catalog and create items. |
| `datafactory` | 7 | Pipelines, Dataflow Gen2 and M (Power Query) query execution. |
| `onelake` | 33 | Workspaces and items, files and directories, the Table API, data access roles, shortcuts and settings. |

## Install

Download a release archive for your platform from [Releases](https://github.com/slachiewicz/fabric-mcp-go/releases), or build from source with Go 1.27.1 or later:

```bash
go install github.com/slachiewicz/fabric-mcp-go/cmd/fabmcp@latest
```

A container image is published as `ghcr.io/slachiewicz/fabric-mcp-go`.

## Configure your MCP client

To register the server in Claude Code:

```bash
claude mcp add fabric -- fabmcp server start
```

For clients configured with `mcp.json`:

```json
{
  "mcpServers": {
    "fabric": {
      "command": "fabmcp",
      "args": ["server", "start"]
    }
  }
}
```

## Sign in

Tools that call Fabric use the Azure default credential chain: environment variables, workload identity, managed identity, then the Azure CLI and Azure Developer CLI.
The simplest setup is to run `az login` first.
If none of those is available, the server opens a browser sign-in.
Set `AZURE_TOKEN_CREDENTIALS` to pin one credential, as with upstream.

Tools marked destructive, such as `onelake_delete-file`, ask for your consent through MCP elicitation before they run.
Clients that don't support elicitation can't run them unless you pass `--dangerously-disable-elicitation`.

## Server options

`fabmcp server start` accepts upstream's options:

| Option | Effect |
|---|---|
| `--mode namespace` | Default. One tool per area; the model discovers commands with `learn=true`. |
| `--mode all` | One tool per command, for example `onelake_list-files`. |
| `--mode single` | One `fabric` tool that routes to every area. |
| `--namespace <area>` | Expose only this area. Repeatable. |
| `--tool <name>` | Expose only this tool; implies `--mode all`. Repeatable. |
| `--read-only` | Expose only read-only tools. |
| `--transport http` | Serve streamable HTTP instead of stdio. |

### HTTP transport

Over HTTP the server authenticates callers against an Entra ID application.
Set `AzureAd__TenantId` and `AzureAd__ClientId`, and optionally `ASPNETCORE_URLS` (default `http://localhost:5000`).
Callers need the `Mcp.Tools.ReadWrite` scope or the `Mcp.Tools.ReadWrite.All` app permission, and the app must issue v2.0 access tokens.

To call Fabric as the signed-in caller rather than as the server's own identity, add `--outgoing-auth-strategy UseOnBehalfOf` and set `AzureAd__ClientSecret`.

For local testing only, `--dangerously-disable-http-incoming-auth` turns authentication off.
The server then listens on `http://127.0.0.1:5001` and refuses non-loopback addresses unless `ALLOW_INSECURE_EXTERNAL_BINDING=true`.

## Differences from upstream

These are deliberate:

- `datafactory_execute-query` decodes Arrow `Date32` columns as days. Upstream reads them as seconds, so every date comes back as 1970-01-01.
- `datafactory_execute-query` polls the query as a long-running operation; upstream reads only the first response.
- There's no telemetry.
- `--mode consolidated` exposes no tools, as upstream does for Fabric.

## Development

```bash
go test ./...
```

`internal/parity` checks the tool list against a snapshot of upstream's in `testdata/ref-tools.json`.
To also compare tool calls, build upstream `main` with the .NET 10 SDK and point `FABMCP_REF` at the binary:

```bash
git clone --depth 1 https://github.com/microsoft/mcp.git
dotnet build mcp/servers/Fabric.Mcp.Server/src/Fabric.Mcp.Server.csproj -c Release
DOTNET_ROOT=~/.dotnet FABMCP_REF=$PWD/mcp/servers/Fabric.Mcp.Server/src/bin/Release/fabmcp \
  go test ./internal/parity/ -v
```

To compare startup time, memory and per-call latency with the upstream build, add `-run Benchmark -parity.bench` to that command, and `-parity.bench.network` to include a live Fabric call.

Tests tagged `live` create and delete items in a workspace you reserve for them:

```bash
FABMCP_E2E_WORKSPACE=<workspace-id> go test -tags live -p 1 ./... -run Live
```

`internal/httpserver`'s live test runs the HTTP transport end to end with on-behalf-of auth.
It needs an Entra app that exposes the `Mcp.Tools.ReadWrite` scope, issues v2.0 tokens, pre-authorizes the Azure CLI (`04b07795-8ddb-461a-bbee-02f9e1bf7b46`) on that scope, and has admin-consented delegated Fabric (Power BI Service) and Azure Storage permissions:

```bash
AzureAd__TenantId=<tenant> AzureAd__ClientId=<app> AzureAd__ClientSecret=<secret> \
  go test -tags live ./internal/httpserver/ -run Live
```

## License

MIT. Portions derived from Microsoft's Fabric MCP Server and the Fabric REST API specifications; see [NOTICE](NOTICE).
