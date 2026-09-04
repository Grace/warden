package main

import (
	"fmt"
	"regexp"
	"strings"
)

// Finding is one place the contract leaks engine vocabulary into published
// text, or publishes something with no explanation attached.
type Finding struct {
	Rule    string
	Problem string
	Detail  string
}

func (f Finding) String() string {
	return fmt.Sprintf("  %-28s %s\n%s%s", f.Rule, f.Problem, strings.Repeat(" ", 31), f.Detail)
}

// Engine rule identifiers tend to look like RL_ACCT_TENURE_LT_90D or
// com.example.rules.TenureCheck — SCREAMING_SNAKE with underscores, or dotted
// package paths. Neither belongs in a sentence shown to a customer.
var (
	screamingSnake = regexp.MustCompile(`\b[A-Z][A-Z0-9]*(?:_[A-Z0-9]+)+\b`)
	dottedPath     = regexp.MustCompile(`\b[a-z][a-z0-9]*(?:\.[a-z][A-Za-z0-9]*){2,}\b`)
)

// Lint looks for the mistake this whole layer exists to prevent: engine
// vocabulary reaching an audience that should never have seen it.
//
// Projection already refuses to emit an unmapped term. Lint catches the case
// projection cannot see — a term that was mapped, but mapped to a string that
// still carries the engine's names inside it.
func Lint(c *Contract) []Finding {
	var out []Finding

	for _, e := range sortedRules(c.Rules) {
		id, r := e.id, e.mapping
		external := r.Audience != Internal

		// The engine's own rule id, copied verbatim into a published field.
		if external {
			if r.ReasonCode == id {
				out = append(out, Finding{id, "reason_code is the engine rule id",
					"consumers would couple to a name you cannot rename later"})
			}
			if strings.Contains(r.Message, id) {
				out = append(out, Finding{id, "message contains the engine rule id",
					fmt.Sprintf("%q", trunc(r.Message))})
			}
			if m := screamingSnake.FindString(r.Message); m != "" {
				out = append(out, Finding{id, "message contains an engine-shaped identifier",
					fmt.Sprintf("%q — did %s come from the rules?", trunc(r.Message), m)})
			}
			if m := dottedPath.FindString(r.Message); m != "" {
				out = append(out, Finding{id, "message contains a dotted path",
					fmt.Sprintf("%q looks like a class or package name", m)})
			}
		}

		// A fact republished under its engine name is a rename that never
		// happened — the contract cannot survive the engine being refactored.
		for name, f := range r.Facts {
			if f.Audience != Internal && f.As == name {
				out = append(out, Finding{id,
					fmt.Sprintf("fact %q is published under its engine name", name),
					"give it a contract name so the engine can rename its own"})
			}
			if strings.Contains(r.Message, name) && f.Audience != Internal {
				out = append(out, Finding{id,
					fmt.Sprintf("message mentions engine fact name %q", name),
					"use the {placeholder} form so the published name is the one in `as`"})
			}
		}

		// A published reason with no message is a code with no meaning. The
		// consumer gets ACCOUNT_TOO_NEW and has to guess, or ask you.
		if external && r.Message == "" {
			out = append(out, Finding{id, "published with no message",
				"a bare code makes the consumer ask a human what it means"})
		}
	}

	for name, o := range c.Outcomes {
		if o.Audience != Internal && o.As == name {
			out = append(out, Finding{name, "outcome is published under its engine token",
				"map it to a contract term so the engine can change its own"})
		}
	}

	return out
}

func trunc(s string) string {
	if len(s) <= 60 {
		return s
	}
	return s[:60] + "…"
}
