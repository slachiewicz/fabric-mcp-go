// Command fabmcp is a Go implementation of the Microsoft Fabric MCP Server.
//
// Usage: fabmcp server start [--transport stdio] [--mode all] [--namespace docs] [--tool name] [--read-only]
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/microsoft/fabric-sdk-go/fabric"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/slachiewicz/fabric-mcp-go/internal/auth"
	"github.com/slachiewicz/fabric-mcp-go/internal/server"
	"github.com/slachiewicz/fabric-mcp-go/internal/tools/core"
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
	transport := fs.String("transport", "stdio", "transport: stdio")
	fs.StringVar(&opts.Mode, "mode", "all", "tool exposure mode: all")
	fs.Var(&ns, "namespace", "expose only this namespace; repeatable")
	fs.Var(&tools, "tool", "expose only this tool; repeatable")
	fs.BoolVar(&opts.ReadOnly, "read-only", false, "expose only read-only tools")
	debug := fs.Bool("debug", false, "debug logging to stderr")
	if err := fs.Parse(args[2:]); err != nil {
		return err
	}
	opts.Namespaces, opts.Tools = ns, tools

	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}
	// stdout carries the protocol; logs go to stderr.
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))

	cred, err := auth.NewCredential()
	if err != nil {
		return err
	}
	client, err := fabric.NewClient(cred, nil, &fabric.ClientOptions{
		ClientOptions: azcore.ClientOptions{Telemetry: policy.TelemetryOptions{ApplicationID: "fabric-mcp-go/" + server.Version}},
	})
	if err != nil {
		return err
	}
	s, err := server.New(opts, docs.New(), core.New(client))
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	switch *transport {
	case "stdio":
		return s.Run(ctx, &mcp.StdioTransport{})
	default:
		return fmt.Errorf("transport %q is not implemented yet", *transport)
	}
}
