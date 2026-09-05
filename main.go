package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const usage = `warden — an anti-corruption layer for rules engines.

A rules engine answers in its own vocabulary: rule ids, fact names, internal
outcome tokens. Handing that to anyone outside the engine couples them to names
you then cannot change. warden translates a decision into a published contract,
scoped to who is asking, and refuses to emit anything the contract has not
declared.

  warden project -contract C -trace T [-audience public]   translate a decision
  warden validate -contract C                              check the contract loads
  warden lint -contract C                                  hunt leaked engine vocabulary
  warden test -contract C features/*.feature               run Gherkin scenarios
  warden oracle -contract C                                 what an adversary learns per query
  warden steps                                             print the step vocabulary
  warden version                                           print version and build

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
	out := "warden " + version
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
	case "test":
		_ = fs.Parse(args)
		os.Exit(runTest(*contract, fs.Args()))
	case "oracle":
		_ = fs.Parse(args)
		os.Exit(runOracle(*contract))
	case "steps":
		fmt.Print(stepVocabulary)
		os.Exit(0)
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

// runTest executes Gherkin scenarios against a contract.
//
// This is how someone who cannot read a ruleset checks that it says what they
// meant: examples they can read, executed against the real projection.
func runTest(contractPath string, patterns []string) int {
	if contractPath == "" {
		return die(fmt.Errorf("-contract is required"))
	}
	if len(patterns) == 0 {
		return die(fmt.Errorf("no .feature files given"))
	}
	c, err := LoadContract(contractPath)
	if err != nil {
		return die(err)
	}

	var paths []string
	for _, p := range patterns {
		matches, err := filepath.Glob(p)
		if err != nil {
			return die(err)
		}
		if len(matches) == 0 {
			return die(fmt.Errorf("no files match %q", p))
		}
		paths = append(paths, matches...)
	}

	passed, failed, err := RunFeatures(os.Stdout, c, paths)
	if err != nil {
		return die(err)
	}
	if failed > 0 {
		fmt.Printf("%d passed, %d FAILED\n", passed, failed)
		return 1
	}
	fmt.Printf("%d scenario(s) passed\n", passed)
	return 0
}

// runOracle reports the disclosure surface at each audience.
func runOracle(contractPath string) int {
	if contractPath == "" {
		return die(fmt.Errorf("-contract is required"))
	}
	c, err := LoadContract(contractPath)
	if err != nil {
		return die(err)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s — what each audience can learn from one decision\n\n", c.Ruleset)
	for _, aud := range []Audience{Public, Partner, Internal} {
		a := AnalyseOracle(c, aud)
		sortObservations(a.Observations)
		a.Report(&b)
	}
	b.WriteString("These are observations, not defects. Publishing a limit is good service\n")
	b.WriteString("in one domain and a gift in another, and only you know which you are in.\n")
	fmt.Print(b.String())
	return 0
}
