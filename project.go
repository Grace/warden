package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strconv"
)

// Trace is what the engine emits, in the engine's own vocabulary: rule
// identifiers, fact names, internal outcome tokens. Nothing in here is fit to
// show anyone outside the system that produced it.
type Trace struct {
	Ruleset string   `json:"ruleset"`
	Version string   `json:"version"`
	Outcome string   `json:"outcome"`
	Firings []Firing `json:"firings"`
}

type Firing struct {
	Rule  string                     `json:"rule"`
	Facts map[string]json.RawMessage `json:"facts"`
}

// Decision is the published form. It is the only thing that crosses the
// boundary, and its shape is a contract: consumers may depend on it, and it
// changes only when contract_version does.
type Decision struct {
	ContractVersion string   `json:"contract_version"`
	Outcome         string   `json:"outcome"`
	Reasons         []Reason `json:"reasons"`

	// Ruleset and Version are engine provenance. Internal viewers get them;
	// nobody else does, because they are the engine's vocabulary and pin
	// consumers to a release cadence that is none of their business.
	Ruleset string `json:"ruleset,omitempty"`
	Version string `json:"version,omitempty"`
}

type Reason struct {
	Code    string            `json:"code"`
	Message string            `json:"message,omitempty"`
	Facts   map[string]string `json:"facts,omitempty"`
}

func LoadTrace(path string) (*Trace, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var t Trace
	if err := dec.Decode(&t); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if t.Outcome == "" {
		return nil, fmt.Errorf("%s: trace has no outcome", path)
	}
	return &t, nil
}

// Project translates an engine trace into the published contract for one
// audience.
//
// It fails closed everywhere it can. An unmapped rule, an unmapped outcome, or
// a message whose facts are not visible to this viewer is an error, not a
// silent omission — because the failure mode that matters here is emitting
// something you did not mean to, and the second-worst is emitting a decision
// whose stated reasons are not the real ones.
func Project(t *Trace, c *Contract, viewer Audience) (*Decision, error) {
	if !viewer.valid() {
		return nil, fmt.Errorf("unknown audience %q", viewer)
	}
	if t.Ruleset != c.Ruleset {
		return nil, fmt.Errorf(
			"trace is from ruleset %q but contract publishes %q", t.Ruleset, c.Ruleset)
	}

	om, ok := c.Outcomes[t.Outcome]
	if !ok {
		return nil, fmt.Errorf(
			"outcome %q is not in the contract; refusing to publish an unmapped outcome",
			t.Outcome)
	}
	if !om.Audience.visibleTo(viewer) {
		return nil, fmt.Errorf(
			"outcome %q is declared %s and cannot be shown to %s",
			t.Outcome, om.Audience, viewer)
	}

	d := &Decision{
		ContractVersion: c.ContractVersion,
		Outcome:         om.As,
		Reasons:         []Reason{},
	}
	if viewer == Internal {
		d.Ruleset, d.Version = t.Ruleset, t.Version
	}

	for _, f := range t.Firings {
		rm, ok := c.Rules[f.Rule]
		if !ok {
			return nil, fmt.Errorf(
				"rule %q fired but is not in the contract; refusing to publish a "+
					"decision whose reasons are incomplete", f.Rule)
		}
		if !rm.Audience.visibleTo(viewer) {
			continue
		}

		facts := map[string]string{}
		for name, fm := range rm.Facts {
			if !fm.Audience.visibleTo(viewer) {
				continue
			}
			raw, present := f.Facts[name]
			if !present {
				continue
			}
			v, err := scalar(raw)
			if err != nil {
				return nil, fmt.Errorf("rule %q fact %q: %w", f.Rule, name, err)
			}
			facts[fm.As] = v
		}

		msg, complete := render(rm.Message, facts)
		if !complete {
			return nil, fmt.Errorf(
				"rule %q: message for %s needs facts that are not visible to that "+
					"audience or absent from the trace", f.Rule, viewer)
		}

		r := Reason{Code: rm.ReasonCode, Message: msg}
		if len(facts) > 0 {
			r.Facts = facts
		}
		d.Reasons = append(d.Reasons, r)
	}

	return d, nil
}

// scalar renders a fact for embedding in a message. Objects and arrays are
// refused: a nested structure is engine shape, and flattening it here would
// leak field names the contract never declared.
func scalar(raw json.RawMessage) (string, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", err
	}
	switch x := v.(type) {
	case string:
		return x, nil
	case bool:
		return strconv.FormatBool(x), nil
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < 1e15 {
			return strconv.FormatInt(int64(x), 10), nil
		}
		return strconv.FormatFloat(x, 'f', -1, 64), nil
	case nil:
		return "", fmt.Errorf("value is null")
	default:
		return "", fmt.Errorf("value is a %T; only scalars can be published", v)
	}
}
