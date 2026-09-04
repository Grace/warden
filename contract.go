package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

// Audience is how widely a term may travel. The zero value is deliberately
// invalid: a term with no declared audience is never published.
//
// Ranks run narrowest-trust to widest-trust. A viewer sees a term when the
// term's rank is at least the viewer's, so `internal` viewers see everything
// and `public` viewers see only what was explicitly published.
type Audience string

const (
	Internal Audience = "internal"
	Partner  Audience = "partner"
	Public   Audience = "public"
)

var audienceRank = map[Audience]int{Internal: 1, Partner: 2, Public: 3}

func (a Audience) valid() bool { _, ok := audienceRank[a]; return ok }

// visibleTo reports whether a term declared for audience `a` may be shown to a
// viewer of audience `viewer`.
func (a Audience) visibleTo(viewer Audience) bool {
	return audienceRank[a] >= audienceRank[viewer]
}

func (a *Audience) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	if !Audience(s).valid() {
		return fmt.Errorf("unknown audience %q (want internal, partner or public)", s)
	}
	*a = Audience(s)
	return nil
}

// Contract is the published vocabulary: the only terms that may leave the
// engine, and who may see each one. Everything absent from it is denied.
type Contract struct {
	ContractVersion string                 `json:"contract_version"`
	Ruleset         string                 `json:"ruleset"`
	Outcomes        map[string]TermMapping `json:"outcomes"`
	Rules           map[string]RuleMapping `json:"rules"`
}

// TermMapping renames one engine token and declares who may see it.
type TermMapping struct {
	As       string   `json:"as"`
	Audience Audience `json:"audience"`
}

// RuleMapping translates one rule firing into a consumer-facing reason.
type RuleMapping struct {
	Audience   Audience               `json:"audience"`
	ReasonCode string                 `json:"reason_code"`
	Message    string                 `json:"message"`
	Facts      map[string]TermMapping `json:"facts"`
}

var placeholder = regexp.MustCompile(`\{([a-z0-9_]+)\}`)

// LoadContract reads a contract and refuses anything it does not fully
// understand. Unknown fields are an error rather than a shrug: a typo in a
// visibility declaration must fail here, at load, in front of whoever made it —
// not silently widen an audience at request time.
func LoadContract(path string) (*Contract, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()

	var c Contract
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := c.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &c, nil
}

func (c *Contract) validate() error {
	if c.ContractVersion == "" {
		return fmt.Errorf("contract_version is required")
	}
	if c.Ruleset == "" {
		return fmt.Errorf("ruleset is required")
	}
	if len(c.Outcomes) == 0 {
		return fmt.Errorf("no outcomes declared; every outcome must be mapped")
	}
	if len(c.Rules) == 0 {
		return fmt.Errorf("no rules declared; every rule that can fire must be mapped")
	}

	for name, o := range c.Outcomes {
		if o.As == "" {
			return fmt.Errorf("outcome %q: `as` is required", name)
		}
		if !o.Audience.valid() {
			return fmt.Errorf("outcome %q: `audience` is required", name)
		}
	}

	for _, r := range sortedRules(c.Rules) {
		if err := r.mapping.validate(r.id); err != nil {
			return err
		}
	}
	return nil
}

func (r RuleMapping) validate(id string) error {
	if !r.Audience.valid() {
		return fmt.Errorf("rule %q: `audience` is required", id)
	}
	if r.ReasonCode == "" {
		return fmt.Errorf("rule %q: `reason_code` is required", id)
	}
	for fact, f := range r.Facts {
		if f.As == "" {
			return fmt.Errorf("rule %q fact %q: `as` is required", id, fact)
		}
		if !f.Audience.valid() {
			return fmt.Errorf("rule %q fact %q: `audience` is required", id, fact)
		}
		// A fact cannot travel further than the reason that carries it.
		if audienceRank[f.Audience] > audienceRank[r.Audience] {
			return fmt.Errorf(
				"rule %q fact %q: declared %s but its rule is %s — a fact cannot "+
					"reach an audience the reason carrying it cannot",
				id, fact, f.Audience, r.Audience)
		}
	}

	// Every placeholder must resolve to a declared fact, or the message would
	// render with a hole in it for whoever is reading.
	published := map[string]bool{}
	for _, f := range r.Facts {
		published[f.As] = true
	}
	for _, m := range placeholder.FindAllStringSubmatch(r.Message, -1) {
		if !published[m[1]] {
			return fmt.Errorf("rule %q: message references {%s}, which no fact declares", id, m[1])
		}
	}
	return nil
}

type ruleEntry struct {
	id      string
	mapping RuleMapping
}

// sortedRules gives deterministic iteration so error messages and lint output
// are stable across runs.
func sortedRules(m map[string]RuleMapping) []ruleEntry {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]ruleEntry, 0, len(ids))
	for _, id := range ids {
		out = append(out, ruleEntry{id, m[id]})
	}
	return out
}

// render substitutes {placeholders} from the facts visible to this viewer.
func render(msg string, facts map[string]string) (string, bool) {
	missing := false
	out := placeholder.ReplaceAllStringFunc(msg, func(tok string) string {
		key := strings.Trim(tok, "{}")
		v, ok := facts[key]
		if !ok {
			missing = true
			return tok
		}
		return v
	})
	return out, !missing
}
