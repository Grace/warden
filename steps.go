package main

import (
	"fmt"
	"strconv"
	"strings"
)

// step interprets one Gherkin line.
//
// The vocabulary is closed on purpose. An unrecognised step is an error, not a
// no-op, because the reader of these files cannot tell the difference between a
// step that asserted something and one that quietly did nothing.
func (s *Scenario) step(raw string, line int) error {
	switch {
	case strings.HasPrefix(raw, "Given"), strings.HasPrefix(raw, "And "),
		strings.HasPrefix(raw, "When"), strings.HasPrefix(raw, "Then"):
	default:
		return fmt.Errorf("step must begin with Given, When, Then or And: %q", raw)
	}

	if m := reRuleset.FindStringSubmatch(raw); m != nil {
		s.trace.Ruleset = m[1]
		return nil
	}
	if m := reOutcome.FindStringSubmatch(raw); m != nil {
		s.trace.Outcome = m[1]
		return nil
	}
	if m := reFires.FindStringSubmatch(raw); m != nil {
		s.trace.Firings = append(s.trace.Firings, Firing{Rule: m[1]})
		return nil
	}
	if m := rePublish.FindStringSubmatch(raw); m != nil {
		a := Audience(m[1])
		if !a.valid() {
			return fmt.Errorf("unknown audience %q; want internal, partner or public", m[1])
		}
		s.audience = a
		return nil
	}

	// --- assertions ---

	if m := reOutIs.FindStringSubmatch(raw); m != nil {
		want := m[1]
		return s.assert(line, raw, func(d *Decision, err error) error {
			if err != nil {
				return fmt.Errorf("publishing was refused: %v", err)
			}
			if d.Outcome != want {
				return fmt.Errorf("outcome is %q, expected %q", d.Outcome, want)
			}
			return nil
		})
	}
	if m := reHasReason.FindStringSubmatch(raw); m != nil {
		code := m[1]
		return s.assert(line, raw, func(d *Decision, err error) error {
			if err != nil {
				return fmt.Errorf("publishing was refused: %v", err)
			}
			if findReason(d, code) == nil {
				return fmt.Errorf("no reason %q; got %s", code, codesOf(d))
			}
			return nil
		})
	}
	if m := reNoReason.FindStringSubmatch(raw); m != nil {
		code := m[1]
		return s.assert(line, raw, func(d *Decision, err error) error {
			if err != nil {
				return fmt.Errorf("publishing was refused: %v", err)
			}
			if findReason(d, code) != nil {
				return fmt.Errorf("reason %q reached this audience and should not have", code)
			}
			return nil
		})
	}
	if m := reMessage.FindStringSubmatch(raw); m != nil {
		code, want := m[1], m[2]
		return s.assert(line, raw, func(d *Decision, err error) error {
			if err != nil {
				return fmt.Errorf("publishing was refused: %v", err)
			}
			r := findReason(d, code)
			if r == nil {
				return fmt.Errorf("no reason %q; got %s", code, codesOf(d))
			}
			if r.Message != want {
				return fmt.Errorf("message is\n      %q\n    expected\n      %q", r.Message, want)
			}
			return nil
		})
	}
	if m := reFact.FindStringSubmatch(raw); m != nil {
		code, name, want := m[1], m[2], m[3]
		return s.assert(line, raw, func(d *Decision, err error) error {
			if err != nil {
				return fmt.Errorf("publishing was refused: %v", err)
			}
			r := findReason(d, code)
			if r == nil {
				return fmt.Errorf("no reason %q; got %s", code, codesOf(d))
			}
			got, ok := r.Facts[name]
			if !ok {
				return fmt.Errorf("reason %q has no published fact %q; it has %s",
					code, name, factNames(r))
			}
			if got != want {
				return fmt.Errorf("fact %q is %q, expected %q", name, got, want)
			}
			return nil
		})
	}
	if m := reNoFact.FindStringSubmatch(raw); m != nil {
		code, name := m[1], m[2]
		return s.assert(line, raw, func(d *Decision, err error) error {
			if err != nil {
				return fmt.Errorf("publishing was refused: %v", err)
			}
			r := findReason(d, code)
			if r == nil {
				return fmt.Errorf("no reason %q; got %s", code, codesOf(d))
			}
			if _, ok := r.Facts[name]; ok {
				return fmt.Errorf("fact %q reached this audience and should not have", name)
			}
			return nil
		})
	}
	if m := reCount.FindStringSubmatch(raw); m != nil {
		want, _ := strconv.Atoi(m[1])
		return s.assert(line, raw, func(d *Decision, err error) error {
			if err != nil {
				return fmt.Errorf("publishing was refused: %v", err)
			}
			if len(d.Reasons) != want {
				return fmt.Errorf("%d reasons, expected %d: %s", len(d.Reasons), want, codesOf(d))
			}
			return nil
		})
	}
	// Refusal is a behaviour worth asserting, not only an error path: a
	// contract that fails closed should have scenarios proving it does.
	if m := reRefusedWh.FindStringSubmatch(raw); m != nil {
		want := m[1]
		return s.assert(line, raw, func(d *Decision, err error) error {
			if err == nil {
				return fmt.Errorf("publishing succeeded, expected a refusal")
			}
			if !strings.Contains(err.Error(), want) {
				return fmt.Errorf("refused with %q, expected it to mention %q", err, want)
			}
			return nil
		})
	}
	if reRefused.MatchString(raw) {
		return s.assert(line, raw, func(d *Decision, err error) error {
			if err == nil {
				return fmt.Errorf("publishing succeeded, expected a refusal")
			}
			return nil
		})
	}

	return fmt.Errorf("unrecognised step: %q\n    run `verdict steps` for the vocabulary", raw)
}

func (s *Scenario) assert(line int, text string, fn func(*Decision, error) error) error {
	s.assertions = append(s.assertions, assertion{line: line, text: text, fn: fn})
	return nil
}

func findReason(d *Decision, code string) *Reason {
	for i := range d.Reasons {
		if d.Reasons[i].Code == code {
			return &d.Reasons[i]
		}
	}
	return nil
}

func codesOf(d *Decision) string {
	if len(d.Reasons) == 0 {
		return "no reasons"
	}
	out := make([]string, 0, len(d.Reasons))
	for _, r := range d.Reasons {
		out = append(out, strconv.Quote(r.Code))
	}
	return strings.Join(out, ", ")
}

func factNames(r *Reason) string {
	if len(r.Facts) == 0 {
		return "none"
	}
	out := make([]string, 0, len(r.Facts))
	for k := range r.Facts {
		out = append(out, strconv.Quote(k))
	}
	sortStrings(out)
	return strings.Join(out, ", ")
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
