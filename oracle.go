package main

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Every decision a system will explain is a query an adversary can make.
//
// Lint asks whether the contract leaks the engine's vocabulary. This asks a
// different question: given a contract that leaks nothing, how much does an
// attacker learn per attempt? A decline reason is a free query, and a caller
// who can submit transactions and read why they failed is running an oracle
// attack — the same shape as a padding oracle, one bit at a time.
//
// None of this is a defect the way a leaked rule id is. Telling a customer
// their parcel exceeds a 30 kg limit is good service; telling a fraudster the
// velocity window is 24 hours is not. The difference is the domain, which this
// tool cannot know. So these are observations with their reasoning attached,
// and the judgement stays with whoever knows what is being defended.

// Observation is one disclosure worth thinking about.
type Observation struct {
	Where  string
	What   string
	Why    string
	Detail string
}

// Analysis is what a caller at one audience can learn.
type Analysis struct {
	Audience     Audience
	Outcomes     int
	Reasons      int
	States       int
	Observations []Observation
}

// Words naming the mechanism rather than the outcome. A code saying what the
// system measured tells an attacker which dimension to vary; one saying what
// happened tells them only that it did.
var mechanismWords = []string{
	"VELOCITY", "FREQUENCY", "THRESHOLD", "SCORE", "RISK", "ANOMALY", "PATTERN",
	"MODEL", "RULE", "LIMIT", "WINDOW", "RATIO", "PERCENTILE", "DEVIATION",
	"BLACKLIST", "DENYLIST", "WATCHLIST", "SANCTION", "MATCH", "FUZZY",
}

// Fact names publishing a boundary rather than a value. The value says what you
// saw; the boundary says where to stop.
var boundaryWords = []string{
	"limit", "max", "min", "threshold", "floor", "ceiling", "cap",
	"maximum", "minimum", "window", "quota", "allowed",
}

var hasDigit = regexp.MustCompile(`[0-9]`)

// AnalyseOracle reports what one audience can distinguish, and why it matters.
func AnalyseOracle(c *Contract, viewer Audience) *Analysis {
	a := &Analysis{Audience: viewer}

	for _, o := range c.Outcomes {
		if o.Audience.visibleTo(viewer) {
			a.Outcomes++
		}
	}

	for _, e := range sortedRules(c.Rules) {
		id, r := e.id, e.mapping
		if !r.Audience.visibleTo(viewer) {
			continue
		}
		a.Reasons++

		if w := containsAny(r.ReasonCode, mechanismWords); w != "" {
			a.Observations = append(a.Observations, Observation{
				Where: r.ReasonCode,
				What:  "names the mechanism, not the outcome",
				Why: "a code saying what you measured tells an attacker which " +
					"dimension to vary; one saying what happened tells them only that it did",
				Detail: fmt.Sprintf("%q appears in the published code (rule %s)", w, id),
			})
		}
		if hasDigit.MatchString(r.Message) {
			a.Observations = append(a.Observations, Observation{
				Where:  r.ReasonCode,
				What:   "the message carries a number",
				Why:    "a published threshold is a boundary an attacker can stop just short of",
				Detail: trunc(r.Message),
			})
		}
		for name, pm := range r.Parameters {
			if !pm.Audience.visibleTo(viewer) {
				continue
			}
			a.Observations = append(a.Observations, Observation{
				Where: r.ReasonCode,
				What:  fmt.Sprintf("publishes the policy parameter %q", pm.As),
				Why: "declared a parameter rather than a fact: the requester did not " +
					"supply it and cannot observe it, so this notice is the only way they get it",
				Detail: fmt.Sprintf("engine term %q, published as %q to %s", name, pm.As, pm.Audience),
			})
		}
		for fact, f := range r.Facts {
			if !f.Audience.visibleTo(viewer) {
				continue
			}
			// Heuristic, and now a fallback: a fact whose published name reads
			// like a boundary was probably a parameter nobody declared.
			if w := containsAny(strings.ToLower(f.As), boundaryWords); w != "" {
				a.Observations = append(a.Observations, Observation{
					Where: r.ReasonCode,
					What:  fmt.Sprintf("publishes a boundary as %q", f.As),
					Why:   "the value you observed says what you saw; the boundary says where to stop",
					Detail: fmt.Sprintf("engine fact %q, published as %q (matched %q) — "+
						"if this is a policy threshold, declare it under `parameters`", fact, f.As, w),
				})
			}
		}
	}

	// Bandwidth: how many distinguishable answers one query can return. Reasons
	// come back as a set rather than a choice, so the states are the subsets —
	// the exponent is the point, and it is why "one more public reason code" is
	// never a small change.
	switch {
	case a.Reasons == 0:
		a.States = a.Outcomes
	case a.Reasons < 24:
		a.States = a.Outcomes * (1 << a.Reasons)
	default:
		a.States = -1
	}
	return a
}

// containsAny matches on whole words rather than substrings.
//
// It used to use strings.Contains, which flagged LIMITED_CREDIT_EXPERIENCE
// because "LIMITED" contains "LIMIT". A lint that cries wolf on a correct
// contract teaches people to skip its output, which costs more than the
// finding was worth.
func containsAny(s string, words []string) string {
	fields := wordSplit.Split(strings.ToUpper(s), -1)
	for _, w := range words {
		want := strings.ToUpper(w)
		for _, f := range fields {
			if f == want {
				return w
			}
		}
	}
	return ""
}

// wordSplit breaks on the separators these identifiers actually use, so
// LIMITED is one word and not a hit for LIMIT.
var wordSplit = regexp.MustCompile(`[^A-Z0-9]+`)

// Report writes the analysis for one audience.
func (a *Analysis) Report(b *strings.Builder) {
	states := fmt.Sprint(a.States)
	if a.States < 0 {
		states = "more than 16 million"
	}
	fmt.Fprintf(b, "%s\n", a.Audience)
	fmt.Fprintf(b, "  %d outcome(s), %d reason(s) — %s distinguishable answers per query\n",
		a.Outcomes, a.Reasons, states)

	if len(a.Observations) == 0 {
		fmt.Fprintf(b, "  nothing further to note at this audience\n\n")
		return
	}
	fmt.Fprintln(b)
	seen := map[string]bool{}
	for _, o := range a.Observations {
		key := o.Where + o.What
		if seen[key] {
			continue
		}
		seen[key] = true
		fmt.Fprintf(b, "  %-26s %s\n", o.Where, o.What)
		fmt.Fprintf(b, "  %-26s %s\n", "", o.Detail)
		fmt.Fprintf(b, "  %-26s %s\n\n", "", o.Why)
	}
}

// sortedObservations keeps output stable.
func sortObservations(o []Observation) {
	sort.SliceStable(o, func(i, j int) bool { return o[i].Where < o[j].Where })
}
