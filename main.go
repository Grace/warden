package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

const usage = `verdict — an anti-corruption layer for rules engines.

A rules engine answers in its own vocabulary: rule ids, fact names, internal
outcome tokens. Handing that to anyone outside the engine couples them to names
you then cannot change. verdict translates a decision into a published contract,
scoped to who is asking, and refuses to emit anything the contract has not
declared.

  verdict project -contract C -trace T [-audience public]   translate a decision
  verdict validate -contract C                              check the contract loads
  verdict lint -contract C                                  hunt leaked engine vocabulary
  verdict version                                           print version and build

Audiences are internal, partner and public. A term is visible to a viewer when
its declared audience is at least as wide as the viewer's, so public terms are
visible to everyone and internal terms only to internal callers.
`

// Stamped at build time with -ldflags; see .goreleaser.yaml.
var (
	version = "dev"
	commit  = ""
	date    = ""
)

func versionString() string {
	out := "verdict " + version
	switch {
	case commit != "" && date != "":
		out += " (" + commit + ", " + date + ")"
	case commit != "":
		out += " (" + commit + ")"
	}
	return out
}

func main() {
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(2)
	}

	cmd, args := os.Args[1], os.Args[2:]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	contract := fs.String("contract", "", "path to the published contract (JSON)")
	trace := fs.String("trace", "", "path to the engine decision trace (JSON)")
	audience := fs.String("audience", string(Public), "internal, partner or public")

	switch cmd {
	case "project":
		_ = fs.Parse(args)
		os.Exit(runProject(*contract, *trace, Audience(*audience)))
	case "validate":
		_ = fs.Parse(args)
		os.Exit(runValidate(*contract))
	case "lint":
		_ = fs.Parse(args)
		os.Exit(runLint(*contract))
	case "version":
		fmt.Println(versionString())
		os.Exit(0)
	case "-h", "--help", "help":
		fmt.Print(usage)
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
}

func die(err error) int {
	fmt.Fprintln(os.Stderr, "error:", err)
	return 1
}

func runProject(contractPath, tracePath string, viewer Audience) int {
	if contractPath == "" || tracePath == "" {
		return die(fmt.Errorf("both -contract and -trace are required"))
	}
	c, err := LoadContract(contractPath)
	if err != nil {
		return die(err)
	}
	t, err := LoadTrace(tracePath)
	if err != nil {
		return die(err)
	}
	d, err := Project(t, c, viewer)
	if err != nil {
		return die(err)
	}
	out, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return die(err)
	}
	fmt.Println(string(out))
	return 0
}

func runValidate(contractPath string) int {
	if contractPath == "" {
		return die(fmt.Errorf("-contract is required"))
	}
	c, err := LoadContract(contractPath)
	if err != nil {
		return die(err)
	}
	fmt.Printf("ok  %s v%s — %d outcomes, %d rules\n",
		c.Ruleset, c.ContractVersion, len(c.Outcomes), len(c.Rules))
	return 0
}

func runLint(contractPath string) int {
	if contractPath == "" {
		return die(fmt.Errorf("-contract is required"))
	}
	c, err := LoadContract(contractPath)
	if err != nil {
		return die(err)
	}
	findings := Lint(c)
	if len(findings) == 0 {
		fmt.Printf("clean  %s — no engine vocabulary in published terms\n", c.Ruleset)
		return 0
	}
	fmt.Printf("LEAKS  %s — %d finding(s)\n\n", c.Ruleset, len(findings))
	for _, f := range findings {
		fmt.Println(f)
	}
	return 1
}
