// Command fabmcp is a Go implementation of the Microsoft Fabric MCP Server.
//
// Usage: fabmcp server start [--transport stdio] [--mode namespace|single|consolidated|all] [--namespace docs] [--tool name] [--read-only]
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/microsoft/fabric-sdk-go/fabric"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/slachiewicz/fabric-mcp-go/internal/auth"
	"github.com/slachiewicz/fabric-mcp-go/internal/httpserver"
	"github.com/slachiewicz/fabric-mcp-go/internal/server"
	"github.com/slachiewicz/fabric-mcp-go/internal/tools/core"
	"github.com/slachiewicz/fabric-mcp-go/internal/tools/datafactory"
	"github.com/slachiewicz/fabric-mcp-go/internal/tools/docs"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "fabmcp:", err)
		os.Exit(1)
	}
}

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func run(args []string) error {
	if len(args) < 2 || args[0] != "server" || args[1] != "start" {
		return fmt.Errorf("usage: fabmcp server start [flags]")
	}
	fs := flag.NewFlagSet("server start", flag.ContinueOnError)
	var opts server.Options
	var ns, tools multiFlag
	transport := fs.String("transport", "stdio", "transport: stdio or http")
	noIncomingAuth := fs.Bool("dangerously-disable-http-incoming-auth", false, "serve HTTP without authenticating callers (loopback only)")
	outgoing := fs.String("outgoing-auth-strategy", "NotSet", "NotSet, UseHostingEnvironmentIdentity or UseOnBehalfOf")
	fs.StringVar(&opts.Mode, "mode", "", "tool exposure mode: namespace (default), single, consolidated, all")
	fs.Var(&ns, "namespace", "expose only this namespace; repeatable")
	fs.Var(&tools, "tool", "expose only this tool; repeatable")
	fs.BoolVar(&opts.ReadOnly, "read-only", false, "expose only read-only tools")
	fs.BoolVar(&opts.DisableElicitation, "dangerously-disable-elicitation", false, "run destructive tools without asking the user for consent")
	debug := fs.Bool("debug", false, "debug logging to stderr")
	if err := fs.Parse(args[2:]); err != nil {
		return err
	}
	opts.Namespaces, opts.Tools = ns, tools
	switch {
	case *transport != "stdio" && *transport != "http":
		return fmt.Errorf("invalid transport %q; valid transports are: stdio, http", *transport)
	case *noIncomingAuth && *transport != "http":
		return fmt.Errorf("the --dangerously-disable-http-incoming-auth option cannot be used with the stdio transport; specify --transport http")
	case *outgoing != "NotSet" && *outgoing != "UseHostingEnvironmentIdentity" && *outgoing != "UseOnBehalfOf":
		return fmt.Errorf("invalid --outgoing-auth-strategy %q", *outgoing)
	case *outgoing == "UseOnBehalfOf" && (*transport != "http" || *noIncomingAuth):
		return fmt.Errorf("the UseOnBehalfOf outgoing authentication strategy requires the server to run in authenticated HTTP mode (--transport http without --dangerously-disable-http-incoming-auth)")
	}

	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}
	// stdout carries the protocol; logs go to stderr.
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))

	var (
		cred azcore.TokenCredential
		err  error
	)
	if *outgoing == "UseOnBehalfOf" {
		c := httpserver.ConfigFromEnv()
		cred = &auth.OnBehalfOf{TenantID: c.TenantID, ClientID: c.ClientID, ClientSecret: os.Getenv("AzureAd__ClientSecret")}
	} else if cred, err = auth.NewCredential(); err != nil {
		return err
	}
	client, err := fabric.NewClient(cred, nil, &fabric.ClientOptions{
		ClientOptions: azcore.ClientOptions{Telemetry: policy.TelemetryOptions{ApplicationID: "fabric-mcp-go/" + server.Version}},
	})
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	s, err := server.New(ctx, opts, docs.New(), core.New(client), datafactory.New(client))
	if err != nil {
		return err
	}
	if *transport == "stdio" {
		return s.Run(ctx, &mcp.StdioTransport{})
	}
	return serveHTTP(ctx, s, !*noIncomingAuth)
}

// serveHTTP serves s over streamable HTTP on ASPNETCORE_URLS, authenticating
// callers against the AzureAd__* Entra ID application unless incomingAuth
// is false.
func serveHTTP(ctx context.Context, s *mcp.Server, incomingAuth bool) error {
	addr, err := httpserver.Address(incomingAuth)
	if err != nil {
		return err
	}
	var h http.Handler
	if incomingAuth {
		cfg := httpserver.ConfigFromEnv()
		verify, err := httpserver.Verifier(ctx, cfg)
		if err != nil {
			return err
		}
		if h, err = httpserver.Handler(s, &cfg, verify); err != nil {
			return err
		}
	} else {
		slog.Warn("incoming HTTP authentication is disabled; every caller can use this server's identity")
		if h, err = httpserver.Handler(s, nil, nil); err != nil {
			return err
		}
	}
	slog.Info("serving MCP over HTTP", "address", addr)
	return httpserver.Serve(ctx, addr, h)
}
