// Command libraos-eval evaluates LibraOS digital employees.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/libraos/libraos-eval/eval"
)

const usage = `libraos-eval evaluates LibraOS digital employees with deterministic checks.

Usage:
  libraos-eval run      --gateway URL (--prompt FILE | --agent NAME) --cases FILE --out FILE [--label TEXT] [--model NAME] [--allow-remote]
  libraos-eval verify   RUN.json...
  libraos-eval compare  BEFORE.json AFTER.json
  libraos-eval report   --out FILE RUN.json...
  libraos-eval serve    --dir DIR [--addr 127.0.0.1:8790]
  libraos-eval install  --gateway URL --id ID --agent-id ID --name TEXT --description TEXT --prompt FILE [--model NAME] [--allow-remote]
  libraos-eval proxy    --listen HOST:PORT --upstream URL --log FILE
  libraos-eval exposure LOG.jsonl
  libraos-eval cases    FILE

The gateway token is read from LIBRAOS_TOKEN. Case evidence is sent to the
gateway and its model provider; non-loopback gateways need --allow-remote.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "run":
		err = runCmd(os.Args[2:])
	case "verify":
		err = verifyCmd(os.Args[2:])
	case "compare":
		err = compareCmd(os.Args[2:])
	case "report":
		err = reportCmd(os.Args[2:])
	case "serve":
		err = serveCmd(os.Args[2:])
	case "install":
		err = installCmd(os.Args[2:])
	case "proxy":
		err = proxyCmd(os.Args[2:])
	case "exposure":
		err = exposureCmd(os.Args[2:])
	case "cases":
		err = casesCmd(os.Args[2:])
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "libraos-eval:", err)
		os.Exit(1)
	}
}

func runCmd(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	gateway := fs.String("gateway", "", "LibraOS gateway URL")
	promptFile := fs.String("prompt", "", "prompt file to install as a tool-free agent")
	agent := fs.String("agent", "", "existing agent to evaluate instead of a prompt")
	casesFile := fs.String("cases", "", "JSONL case file")
	out := fs.String("out", "", "run record to write")
	label := fs.String("label", "", "free-text label")
	model := fs.String("model", "", "model label to record when the gateway does not report one")
	maxTokens := fs.Int("max-output-tokens", 2048, "max output tokens for an installed prompt")
	timeout := fs.Duration("timeout", 2*time.Hour, "overall timeout")
	allowRemote := fs.Bool("allow-remote", false, "allow a non-loopback gateway")
	_ = fs.Parse(args)
	if *out == "" || *casesFile == "" || (*promptFile == "") == (*agent == "") {
		return fmt.Errorf("run needs --cases, --out and exactly one of --prompt or --agent")
	}
	cases, casesDigest, err := eval.LoadCases(*casesFile)
	if err != nil {
		return err
	}
	g, err := eval.NewGateway(*gateway, os.Getenv("LIBRAOS_TOKEN"), *allowRemote)
	if err != nil {
		return err
	}
	target := eval.Target{Agent: *agent, Model: *model, MaxOutputTokens: *maxTokens}
	if *promptFile != "" {
		raw, err := os.ReadFile(*promptFile)
		if err != nil {
			return err
		}
		target.Prompt = strings.TrimSpace(string(raw))
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	r, err := eval.Execute(ctx, g, target, cases, casesDigest, *label, func(done, total int) { fmt.Fprintf(os.Stderr, "case %d/%d\n", done, total) })
	if err != nil {
		return err
	}
	if err = eval.Save(r, *out); err != nil {
		return err
	}
	fmt.Printf("%s: %d/%d passed, citations anchored %d/%d, mean latency %d ms, agent %s\n", *out, r.Summary.Passed, r.Summary.Total, r.Summary.CitationsAnchored, r.Summary.CitationsReturned, r.Summary.MeanLatencyMS, r.Agent)
	return nil
}

func verifyCmd(files []string) error {
	if len(files) == 0 {
		return fmt.Errorf("verify needs run files")
	}
	failed := false
	for _, f := range files {
		r, err := eval.Load(f)
		if err == nil {
			err = eval.Verify(r)
		}
		if err != nil {
			failed = true
			fmt.Printf("%s: FAILED %v\n", f, err)
		} else {
			fmt.Printf("%s: verified (%d/%d passed)\n", f, r.Summary.Passed, r.Summary.Total)
		}
	}
	if failed {
		return fmt.Errorf("verification failed")
	}
	return nil
}

func compareCmd(files []string) error {
	if len(files) != 2 {
		return fmt.Errorf("compare needs BEFORE.json AFTER.json")
	}
	var runs [2]*eval.Run
	for i, f := range files {
		r, err := eval.Load(f)
		if err != nil {
			return err
		}
		if err = eval.Verify(r); err != nil {
			return fmt.Errorf("%s: %w", f, err)
		}
		runs[i] = r
	}
	c, err := eval.Compare(runs[0], runs[1])
	if err != nil {
		return err
	}
	fmt.Printf("passed %d/%d -> %d/%d; citations anchored %.0f%% -> %.0f%%\n", c.BeforePassed, c.Total, c.AfterPassed, c.Total, 100*c.BeforeAnchoredRatio, 100*c.AfterAnchoredRatio)
	for _, d := range c.Cases {
		if d.Change != "same" {
			fmt.Printf("  %-9s %s (%s)\n", d.Change, d.Title, d.CaseID)
		}
	}
	fmt.Println(c.Note)
	return nil
}

func reportCmd(args []string) error {
	fs := flag.NewFlagSet("report", flag.ExitOnError)
	out := fs.String("out", "report.html", "HTML file to write")
	_ = fs.Parse(args)
	if fs.NArg() == 0 {
		return fmt.Errorf("report needs run files")
	}
	f, err := os.Create(*out)
	if err != nil {
		return err
	}
	defer f.Close()
	return eval.WriteReport(f, fs.Args())
}

func serveCmd(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	dir := fs.String("dir", ".", "directory of run records")
	addr := fs.String("addr", "127.0.0.1:8790", "loopback listen address")
	_ = fs.Parse(args)
	fmt.Printf("libraos-eval UI: http://%s (runs in %s)\n", *addr, *dir)
	return eval.Serve(*addr, *dir)
}

func installCmd(args []string) error {
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	gateway := fs.String("gateway", "", "LibraOS gateway URL")
	e := eval.Employee{}
	fs.StringVar(&e.ID, "id", "", "employee id (kebab-case)")
	fs.StringVar(&e.AgentID, "agent-id", "", "bound agent id (kebab-case)")
	fs.StringVar(&e.DisplayName, "name", "", "display name")
	fs.StringVar(&e.Description, "description", "", "one-line description")
	fs.StringVar(&e.Model, "model", "", "agent model (default: gateway default)")
	fs.IntVar(&e.MaxOutputTokens, "max-output-tokens", 2048, "max output tokens")
	promptFile := fs.String("prompt", "", "agent prompt file")
	allowRemote := fs.Bool("allow-remote", false, "allow a non-loopback gateway")
	_ = fs.Parse(args)
	raw, err := os.ReadFile(*promptFile)
	if err != nil {
		return err
	}
	e.Prompt = string(raw)
	g, err := eval.NewGateway(*gateway, os.Getenv("LIBRAOS_TOKEN"), *allowRemote)
	if err != nil {
		return err
	}
	digest, err := g.InstallEmployee(context.Background(), e)
	if err != nil {
		return err
	}
	out, _ := json.Marshal(map[string]string{"employee_id": e.ID, "agent_id": e.AgentID, "installed_agent_sha256": digest})
	fmt.Println(string(out))
	return nil
}

func proxyCmd(args []string) error {
	fs := flag.NewFlagSet("proxy", flag.ExitOnError)
	listen := fs.String("listen", "127.0.0.1:8791", "listen address")
	upstream := fs.String("upstream", "", "OpenAI-compatible model server base URL")
	logPath := fs.String("log", "model-requests.jsonl", "JSONL log of model requests")
	_ = fs.Parse(args)
	h, err := eval.RecordingProxy(*upstream, *logPath)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	fmt.Printf("recording proxy %s -> %s, log %s\n", ln.Addr(), *upstream, *logPath)
	return http.Serve(ln, h)
}

func exposureCmd(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("exposure needs a proxy log")
	}
	requests, maxTools, models, err := eval.ToolExposure(args[0])
	if err != nil {
		return err
	}
	fmt.Printf("%d model requests; models %v; max tool definitions in one request: %d\n", requests, models, maxTools)
	if maxTools > 0 {
		return fmt.Errorf("tool definitions were exposed to the model")
	}
	return nil
}

func casesCmd(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("cases needs a case file")
	}
	cases, digest, err := eval.LoadCases(args[0])
	if err != nil {
		return err
	}
	kinds := map[string]int{}
	for _, c := range cases {
		kinds[c.Kind]++
	}
	fmt.Printf("%d valid cases %v, sha256 %s\n", len(cases), kinds, digest)
	return nil
}
