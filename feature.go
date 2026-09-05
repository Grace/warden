package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// Gherkin scenarios are how someone who cannot read the ruleset checks that it
// says what they meant.
//
// The step vocabulary is fixed and wired to this tool, so a .feature file is
// executable on its own. Standard Cucumber asks you to write step definitions
// in code, which defeats the purpose here — the person who needs to read these
// is the one who cannot read Go.
//
// A scenario necessarily has one foot on each side of the boundary, and that is
// the point rather than a compromise. The Given steps describe an engine trace,
// in the engine's vocabulary, written by whoever owns the ruleset. The Then
// steps describe what one audience sees, in contract vocabulary, read by
// whoever owns the contract. The scenario is the boundary, written down.

// Scenario is one example.
type Scenario struct {
	Feature string
	Name    string
	Line    int

	trace    Trace
	audience Audience

	assertions []assertion
}

type assertion struct {
	line int
	text string
	fn   func(*Decision, error) error
}

// Feature is a parsed .feature file.
type Feature struct {
	Path      string
	Name      string
	Scenarios []*Scenario
}

var (
	reFeature   = regexp.MustCompile(`^Feature:\s*(.*)$`)
	reScenario  = regexp.MustCompile(`^Scenario:\s*(.*)$`)
	reRuleset   = regexp.MustCompile(`^(?:Given|And) the ruleset "([^"]*)"$`)
	reOutcome   = regexp.MustCompile(`^(?:Given|And) the outcome "([^"]*)"$`)
	reFires     = regexp.MustCompile(`^(?:Given|And) rule "([^"]*)" fires(\s+with:)?$`)
	rePublish   = regexp.MustCompile(`^When published to "([^"]*)"$`)
	reOutIs     = regexp.MustCompile(`^(?:Then|And) the outcome is "([^"]*)"$`)
	reHasReason = regexp.MustCompile(`^(?:Then|And) reason "([^"]*)" is present$`)
	reNoReason  = regexp.MustCompile(`^(?:Then|And) reason "([^"]*)" is absent$`)
	reMessage   = regexp.MustCompile(`^(?:Then|And) the message for "([^"]*)" is "(.*)"$`)
	reFact      = regexp.MustCompile(`^(?:Then|And) reason "([^"]*)" has fact "([^"]*)" as "(.*)"$`)
	reNoFact    = regexp.MustCompile(`^(?:Then|And) reason "([^"]*)" has no fact "([^"]*)"$`)
	reCount     = regexp.MustCompile(`^(?:Then|And) there (?:is|are) (\d+) reasons?$`)
	reRefused   = regexp.MustCompile(`^(?:Then|And) publishing is refused$`)
	reRefusedWh = regexp.MustCompile(`^(?:Then|And) publishing is refused because "(.*)"$`)
	reTableRow  = regexp.MustCompile(`^\|(.*)\|$`)
)

// ParseFeature reads one .feature file.
//
// Unknown steps are an error rather than a skip. A scenario that silently does
// not assert what its author believed is worse than no scenario, and this is a
// tool for people who cannot check the difference themselves.
func ParseFeature(path string) (*Feature, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	feat := &Feature{Path: path}
	var cur *Scenario
	var pendingRule string

	sc := bufio.NewScanner(f)
	for line := 1; sc.Scan(); line++ {
		raw := strings.TrimSpace(sc.Text())
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}

		if m := reFeature.FindStringSubmatch(raw); m != nil {
			feat.Name = strings.TrimSpace(m[1])
			continue
		}
		if m := reScenario.FindStringSubmatch(raw); m != nil {
			cur = &Scenario{Feature: feat.Name, Name: strings.TrimSpace(m[1]), Line: line,
				audience: Public, trace: Trace{Firings: []Firing{}}}
			feat.Scenarios = append(feat.Scenarios, cur)
			pendingRule = ""
			continue
		}
		if cur == nil {
			return nil, fmt.Errorf("%s:%d: %q appears before any Scenario", path, line, raw)
		}

		// A table row belongs to the rule named on the preceding step.
		if m := reTableRow.FindStringSubmatch(raw); m != nil {
			if pendingRule == "" {
				return nil, fmt.Errorf("%s:%d: table row with no preceding `fires with:`", path, line)
			}
			cells := strings.Split(strings.Trim(m[1], "|"), "|")
			if len(cells) != 2 {
				return nil, fmt.Errorf("%s:%d: want two columns, fact and value", path, line)
			}
			name := strings.TrimSpace(cells[0])
			value := strings.TrimSpace(cells[1])
			if err := cur.addFact(pendingRule, name, value); err != nil {
				return nil, fmt.Errorf("%s:%d: %w", path, line, err)
			}
			continue
		}
		pendingRule = ""

		if err := cur.step(raw, line); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, line, err)
		}
		if m := reFires.FindStringSubmatch(raw); m != nil && m[2] != "" {
			pendingRule = m[1]
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return feat, nil
}

func (s *Scenario) addFact(rule, name, value string) error {
	for i := range s.trace.Firings {
		if s.trace.Firings[i].Rule != rule {
			continue
		}
		if s.trace.Firings[i].Facts == nil {
			s.trace.Firings[i].Facts = map[string]json.RawMessage{}
		}
		s.trace.Firings[i].Facts[name] = jsonScalar(value)
		return nil
	}
	return fmt.Errorf("no firing for rule %q", rule)
}

// jsonScalar renders a table cell as JSON, keeping numbers and booleans as
// themselves so a scenario reads naturally and the projection sees what the
// engine would have sent.
func jsonScalar(v string) json.RawMessage {
	if v == "true" || v == "false" {
		return json.RawMessage(v)
	}
	if _, err := strconv.ParseFloat(v, 64); err == nil {
		return json.RawMessage(v)
	}
	b, _ := json.Marshal(v)
	return b
}
