package main

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
)

// Result is the outcome of one scenario.
type Result struct {
	Scenario *Scenario
	Failures []Failure
}

// Failure is one assertion that did not hold.
type Failure struct {
	Line int
	Step string
	Why  string
}

func (r Result) Passed() bool { return len(r.Failures) == 0 }

// Run projects a scenario's trace and checks its assertions.
//
// Every assertion runs, rather than stopping at the first failure. Someone who
// cannot read the ruleset is trying to work out what it does, and three
// findings tell them more than one.
func (s *Scenario) Run(c *Contract) Result {
	res := Result{Scenario: s}

	d, err := Project(&s.trace, c, s.audience)
	for _, a := range s.assertions {
		if e := a.fn(d, err); e != nil {
			res.Failures = append(res.Failures, Failure{Line: a.line, Step: a.text, Why: e.Error()})
		}
	}
	if len(s.assertions) == 0 {
		res.Failures = append(res.Failures, Failure{
			Line: s.Line, Step: "Scenario: " + s.Name,
			Why: "no Then steps, so this scenario asserts nothing",
		})
	}
	return res
}

// RunFeatures parses and runs every file, writing a report.
func RunFeatures(w io.Writer, c *Contract, paths []string) (passed, failed int, err error) {
	sort.Strings(paths)

	for _, path := range paths {
		feat, perr := ParseFeature(path)
		if perr != nil {
			return passed, failed + 1, perr
		}
		fmt.Fprintf(w, "%s\n", feat.Name)

		for _, sc := range feat.Scenarios {
			res := sc.Run(c)
			if res.Passed() {
				passed++
				fmt.Fprintf(w, "  ok    %s\n", sc.Name)
				continue
			}
			failed++
			fmt.Fprintf(w, "  FAIL  %s\n", sc.Name)
			for _, f := range res.Failures {
				fmt.Fprintf(w, "        %s:%d  %s\n", filepath.Base(path), f.Line, f.Step)
				for _, line := range strings.Split(f.Why, "\n") {
					fmt.Fprintf(w, "          %s\n", line)
				}
			}
		}
		fmt.Fprintln(w)
	}
	return passed, failed, nil
}

const stepVocabulary = `Steps verdict understands. The vocabulary is closed: an unrecognised step is
an error, because a scenario that quietly asserts nothing is worse than none.

Setting up an engine trace — the engine's vocabulary, written by whoever owns
the ruleset:

  Given the ruleset "shipping-eligibility"
  And the outcome "DENY"
  And rule "RL_PKG_MASS_OVER_LIMIT" fires
  And rule "RL_PKG_MASS_OVER_LIMIT" fires with:
    | PkgMassKg | 34.5 |
    | MaxMassKg | 30   |

Publishing:

  When published to "public"          (or "partner", or "internal")

Checking what that audience sees — contract vocabulary, read by whoever owns
the contract:

  Then the outcome is "not_eligible"
  And reason "PACKAGE_TOO_HEAVY" is present
  And reason "BELOW_MARGIN_FLOOR" is absent
  And the message for "PACKAGE_TOO_HEAVY" is "This package is 34.5 kg; the limit is 30 kg."
  And reason "PACKAGE_TOO_HEAVY" has fact "limit" as "30"
  And reason "DESTINATION_NOT_SERVED" has no fact "zone"
  And there are 2 reasons

Checking that it refuses, which a contract that fails closed should prove:

  Then publishing is refused
  And publishing is refused because "not in the contract"
`
