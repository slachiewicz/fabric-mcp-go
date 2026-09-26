package parity

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestBenchmark compares startup time, memory and per-call latency of our
// server and the reference. It runs only with -parity.bench and FABMCP_REF:
//
//	FABMCP_REF=... go test ./internal/parity/ -run Benchmark -parity.bench -v
//
// The docs tools need no tenant; -parity.bench.network adds a live
// core_search-catalog call. -parity.bench.out writes the Markdown table to
// a file as well as the test log.
var (
	bench        = flag.Bool("parity.bench", false, "run TestBenchmark")
	benchNetwork = flag.Bool("parity.bench.network", false, "include a live Fabric call in TestBenchmark")
	benchOut     = flag.String("parity.bench.out", "", "also write TestBenchmark's Markdown table to this file")
)

type benchCall struct {
	name string
	args map[string]any
	n    int
}

var benchCalls = []benchCall{
	{"docs_list-item-types", map[string]any{}, 200},
	{"docs_item-definitions", map[string]any{"item-type": "lakehouse"}, 200},
	{"docs_item-api-spec", map[string]any{"item-type": "lakehouse"}, 200},
	{"docs_platform-api-spec", map[string]any{}, 100},
}

type benchResult struct {
	coldStart          []time.Duration
	rssStart, rssAfter float64 // MB; -1 when ps isn't available
	latency            map[string][]time.Duration
}

func TestBenchmark(t *testing.T) {
	if !*bench {
		t.Skip("run with -parity.bench")
	}
	refPath := os.Getenv("FABMCP_REF")
	if refPath == "" {
		t.Skip("set FABMCP_REF to the reference fabmcp binary")
	}
	calls := slices.Clone(benchCalls)
	if *benchNetwork {
		calls = append(calls, benchCall{"core_search-catalog", map[string]any{"search": "polytable_lh", "page-size": 5}, 20})
	}
	ours := runBench(t, buildOurBinary(t), nil, calls)
	ref := runBench(t, refPath, refEnv(), calls)

	var b strings.Builder
	fmt.Fprintf(&b, "| Measure | Go | .NET reference |\n|---|---|---|\n")
	fmt.Fprintf(&b, "| Cold start, median of %d | %s | %s |\n", len(ours.coldStart), ms(pct(ours.coldStart, 0.5)), ms(pct(ref.coldStart, 0.5)))
	fmt.Fprintf(&b, "| Memory after start | %s | %s |\n", mb(ours.rssStart), mb(ref.rssStart))
	fmt.Fprintf(&b, "| Memory after the calls | %s | %s |\n", mb(ours.rssAfter), mb(ref.rssAfter))
	for _, c := range calls {
		o, r := ours.latency[c.name], ref.latency[c.name]
		fmt.Fprintf(&b, "| `%s` median / p95, %d calls | %s / %s | %s / %s |\n", c.name, c.n,
			ms(pct(o, 0.5)), ms(pct(o, 0.95)), ms(pct(r, 0.5)), ms(pct(r, 0.95)))
	}
	t.Log("\n" + b.String())
	if *benchOut != "" {
		if err := os.WriteFile(*benchOut, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func runBench(t *testing.T, path string, env []string, calls []benchCall) benchResult {
	t.Helper()
	ctx := t.Context()
	res := benchResult{latency: map[string][]time.Duration{}}
	for range 10 {
		sess, _, d := benchStart(t, ctx, path, env)
		res.coldStart = append(res.coldStart, d)
		_ = sess.Close()
	}
	sess, cmd, _ := benchStart(t, ctx, path, env)
	defer func() { _ = sess.Close() }()
	res.rssStart = rssMB(cmd.Process.Pid)
	for _, c := range calls {
		for range 5 { // warm-up
			_, _ = sess.CallTool(ctx, &mcp.CallToolParams{Name: c.name, Arguments: c.args})
		}
		for range c.n {
			t0 := time.Now()
			r, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: c.name, Arguments: c.args})
			res.latency[c.name] = append(res.latency[c.name], time.Since(t0))
			if err != nil || r.IsError {
				t.Fatalf("%s %s: err=%v isError=%v", path, c.name, err, r != nil && r.IsError)
			}
		}
	}
	res.rssAfter = rssMB(cmd.Process.Pid)
	return res
}

// benchStart launches a server and returns once initialize and tools/list
// have completed, with the time that took.
func benchStart(t *testing.T, ctx context.Context, path string, env []string) (*mcp.ClientSession, *exec.Cmd, time.Duration) {
	t.Helper()
	cmd := exec.Command(path, serverArgs()...)
	cmd.Env = env
	t0 := time.Now()
	sess, err := mcp.NewClient(&mcp.Implementation{Name: "fabmcp-bench"}, nil).Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("start %s: %v", path, err)
	}
	if _, err := sess.ListTools(ctx, nil); err != nil {
		t.Fatalf("list tools on %s: %v", path, err)
	}
	return sess, cmd, time.Since(t0)
}

// rssMB returns the process's resident memory from ps, or -1.
func rssMB(pid int) float64 {
	out, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return -1
	}
	kb, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil {
		return -1
	}
	return kb / 1024
}

func pct(d []time.Duration, p float64) time.Duration {
	s := slices.Clone(d)
	slices.Sort(s)
	return s[int(float64(len(s)-1)*p)]
}

func ms(d time.Duration) string { return fmt.Sprintf("%.1f ms", float64(d.Microseconds())/1000) }

func mb(v float64) string {
	if v < 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.0f MB", v)
}
