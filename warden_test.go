package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func load(t *testing.T) (*Contract, *Trace) {
	t.Helper()
	c, err := LoadContract("testdata/contract.json")
	if err != nil {
		t.Fatalf("contract: %v", err)
	}
	tr, err := LoadTrace("testdata/trace.json")
	if err != nil {
		t.Fatalf("trace: %v", err)
	}
	return c, tr
}

func codes(d *Decision) []string {
	out := make([]string, 0, len(d.Reasons))
	for _, r := range d.Reasons {
		out = append(out, r.Code)
	}
	return out
}

// The point of the whole layer: a public caller learns why, and learns nothing
// about the engine or about the reasons that were never theirs to see.
func TestPublicSeesOnlyPublishedReasons(t *testing.T) {
	c, tr := load(t)
	d, err := Project(tr, c, Public)
	if err != nil {
		t.Fatalf("project: %v", err)
	}

	if d.Outcome != "not_eligible" {
		t.Errorf("outcome = %q, want not_eligible", d.Outcome)
	}
	if got := codes(d); len(got) != 2 {
		t.Fatalf("reasons = %v, want the two public ones", got)
	}
	for _, r := range d.Reasons {
		if r.Code == "BELOW_MARGIN_FLOOR" {
			t.Error("internal margin reason reached a public caller")
		}
	}
	if d.Ruleset != "" || d.Version != "" {
		t.Errorf("engine provenance leaked to public: %q %q", d.Ruleset, d.Version)
	}

	// The zone code is internal even though its reason is public.
	blob, _ := json.Marshal(d)
	if strings.Contains(string(blob), "Z9") {
		t.Errorf("internal fact leaked into public projection: %s", blob)
	}
	if !strings.Contains(string(blob), "Z1-Z4") {
		t.Errorf("public fact missing from projection: %s", blob)
	}
}

func TestInternalSeesEverything(t *testing.T) {
	c, tr := load(t)
	d, err := Project(tr, c, Internal)
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	if len(d.Reasons) != 3 {
		t.Errorf("internal reasons = %v, want all three", codes(d))
	}
	if d.Ruleset == "" || d.Version == "" {
		t.Error("internal projection should carry engine provenance")
	}
}

func TestMessagesRenderWithVisibleFacts(t *testing.T) {
	c, tr := load(t)
	d, err := Project(tr, c, Public)
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	var found bool
	for _, r := range d.Reasons {
		if r.Code != "PACKAGE_TOO_HEAVY" {
			continue
		}
		found = true
		if want := "This package is 34.5 kg; the limit is 30 kg."; r.Message != want {
			t.Errorf("message = %q, want %q", r.Message, want)
		}
	}
	if !found {
		t.Fatal("PACKAGE_TOO_HEAVY missing")
	}
}

// Fail closed: a rule the contract has never heard of must stop the whole
// projection. Publishing a decision with a reason silently dropped would state
// a conclusion whose stated causes are not the real ones.
func TestUnmappedRuleIsRefused(t *testing.T) {
	c, tr := load(t)
	tr.Firings = append(tr.Firings, Firing{Rule: "RL_BRAND_NEW_RULE"})
	if _, err := Project(tr, c, Public); err == nil {
		t.Fatal("expected refusal for an unmapped rule")
	} else if !strings.Contains(err.Error(), "RL_BRAND_NEW_RULE") {
		t.Errorf("error should name the offending rule, got %v", err)
	}
}

func TestUnmappedOutcomeIsRefused(t *testing.T) {
	c, tr := load(t)
	tr.Outcome = "ESCALATE"
	if _, err := Project(tr, c, Public); err == nil {
		t.Fatal("expected refusal for an unmapped outcome")
	}
}

func TestOutcomeAudienceIsEnforced(t *testing.T) {
	c, tr := load(t)
	tr.Outcome = "HOLD" // declared partner
	if _, err := Project(tr, c, Public); err == nil {
		t.Fatal("a partner-only outcome must not reach a public caller")
	}
	if _, err := Project(tr, c, Partner); err != nil {
		t.Errorf("partner should see a partner outcome: %v", err)
	}
}

func TestTraceFromAnotherRulesetIsRefused(t *testing.T) {
	c, tr := load(t)
	tr.Ruleset = "something-else"
	if _, err := Project(tr, c, Public); err == nil {
		t.Fatal("expected refusal when trace and contract disagree on ruleset")
	}
}

// A misspelled visibility key must fail at load, in front of whoever typed it,
// rather than defaulting to something wider at request time.
func TestUnknownFieldsAreRejectedAtLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	body := `{
	  "contract_version": "1",
	  "ruleset": "x",
	  "outcomes": {"A": {"as": "a", "audience": "public"}},
	  "rules": {"R": {"audiance": "public", "reason_code": "C"}}
	}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadContract(path)
	if err == nil {
		t.Fatal("expected a typo'd `audiance` key to be refused")
	}
	if !strings.Contains(err.Error(), "audiance") {
		t.Errorf("error should name the unknown field, got %v", err)
	}
}

func TestInvalidAudienceIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aud.json")
	body := `{
	  "contract_version": "1",
	  "ruleset": "x",
	  "outcomes": {"A": {"as": "a", "audience": "everyone"}},
	  "rules": {"R": {"audience": "public", "reason_code": "C"}}
	}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadContract(path); err == nil {
		t.Fatal("expected an unknown audience to be refused")
	}
}

// A fact cannot be declared wider than the reason carrying it — otherwise a
// public fact would ride out on an internal reason, or appear to.
func TestFactCannotOutrankItsRule(t *testing.T) {
	r := RuleMapping{
		Audience:   Internal,
		ReasonCode: "X",
		Facts:      map[string]TermMapping{"F": {As: "f", Audience: Public}},
	}
	if err := r.validate("RL_X"); err == nil {
		t.Fatal("expected refusal: public fact on an internal rule")
	}
}

func TestMessagePlaceholdersMustResolve(t *testing.T) {
	r := RuleMapping{
		Audience:   Public,
		ReasonCode: "X",
		Message:    "limit is {limit}",
		Facts:      map[string]TermMapping{"F": {As: "other", Audience: Public}},
	}
	if err := r.validate("RL_X"); err == nil {
		t.Fatal("expected refusal: {limit} matches no declared fact")
	}
}

func TestLintCatchesLeakedVocabulary(t *testing.T) {
	c, err := LoadContract("testdata/leaky.json")
	if err != nil {
		t.Fatalf("leaky contract should still load: %v", err)
	}
	f := Lint(c)
	if len(f) == 0 {
		t.Fatal("lint found nothing in a deliberately leaky contract")
	}

	var sawRuleID, sawDotted, sawEngineFact, sawBareCode, sawOutcome bool
	for _, x := range f {
		switch {
		case strings.Contains(x.Problem, "reason_code is the engine rule id"):
			sawRuleID = true
		case strings.Contains(x.Problem, "dotted path"):
			sawDotted = true
		case strings.Contains(x.Problem, "published under its engine name"):
			sawEngineFact = true
		case strings.Contains(x.Problem, "no message"):
			sawBareCode = true
		case strings.Contains(x.Problem, "outcome is published under its engine token"):
			sawOutcome = true
		}
	}
	for name, ok := range map[string]bool{
		"engine rule id as reason_code":  sawRuleID,
		"dotted class path in message":   sawDotted,
		"fact under its engine name":     sawEngineFact,
		"published code with no message": sawBareCode,
		"outcome under its engine token": sawOutcome,
	} {
		if !ok {
			t.Errorf("lint missed: %s", name)
		}
	}
}

func TestCleanContractLintsClean(t *testing.T) {
	c, _ := load(t)
	if f := Lint(c); len(f) != 0 {
		t.Errorf("clean contract produced findings: %v", f)
	}
}

func TestScalarRefusesStructures(t *testing.T) {
	if _, err := scalar(json.RawMessage(`{"a":1}`)); err == nil {
		t.Error("objects must not be publishable")
	}
	if _, err := scalar(json.RawMessage(`[1,2]`)); err == nil {
		t.Error("arrays must not be publishable")
	}
	if got, err := scalar(json.RawMessage(`30`)); err != nil || got != "30" {
		t.Errorf("integral floats should render without a decimal, got %q %v", got, err)
	}
}

// contractWith builds a minimal contract around one rule, for the
// fact/parameter tests.
func contractWith(t *testing.T, rule string) *Contract {
	t.Helper()
	body := `{"contract_version":"1","ruleset":"r",
	  "outcomes":{"DENY":{"as":"not_eligible","audience":"public"}},
	  "rules":{"RL_X":` + rule + `}}`
	path := t.TempDir() + "/c.json"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadContract(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return c
}

func TestParameterCannotAlsoBeAFact(t *testing.T) {
	body := `{"contract_version":"1","ruleset":"r",
	  "outcomes":{"DENY":{"as":"no","audience":"public"}},
	  "rules":{"RL_X":{"audience":"public","reason_code":"X",
	    "facts":{"MaxKg":{"as":"limit","audience":"public"}},
	    "parameters":{"MaxKg":{"as":"cap","audience":"internal"}}}}}`
	path := t.TempDir() + "/c.json"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadContract(path)
	if err == nil || !strings.Contains(err.Error(), "both a fact and a parameter") {
		t.Fatalf("want a provenance conflict, got %v", err)
	}
}

func TestParametersRenderIntoMessages(t *testing.T) {
	// A parameter is still a term the message may reference; the split is about
	// what publishing it costs, not about whether it can be said at all.
	c := contractWith(t, `{"audience":"internal","reason_code":"TOO_HEAVY",
	  "message":"This is {mass} kg; the limit is {limit} kg.",
	  "facts":{"PkgKg":{"as":"mass","audience":"internal"}},
	  "parameters":{"MaxKg":{"as":"limit","audience":"internal"}}}`)
	if _, ok := c.Rules["RL_X"]; !ok {
		t.Fatal("rule missing")
	}
	if got := len(c.Rules["RL_X"].terms()); got != 2 {
		t.Errorf("terms() should carry facts and parameters together, got %d", got)
	}
}

func TestParameterCannotOutrankItsRule(t *testing.T) {
	body := `{"contract_version":"1","ruleset":"r",
	  "outcomes":{"DENY":{"as":"no","audience":"public"}},
	  "rules":{"RL_X":{"audience":"internal","reason_code":"X",
	    "parameters":{"MaxKg":{"as":"limit","audience":"public"}}}}}`
	path := t.TempDir() + "/c.json"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadContract(path); err == nil {
		t.Fatal("a parameter must not outrank the reason carrying it")
	}
}

func TestLintFlagsPublishedParameters(t *testing.T) {
	// The finding the split exists to make statable.
	c := contractWith(t, `{"audience":"public","reason_code":"TOO_HEAVY",
	  "message":"The limit is {limit} kg.",
	  "parameters":{"MaxKg":{"as":"limit","audience":"public"}}}`)
	var found bool
	for _, f := range Lint(c) {
		if strings.Contains(f.Problem, `parameter "MaxKg" is published`) {
			found = true
			if !strings.Contains(f.Detail, "cannot set or observe") {
				t.Errorf("finding should say why it is expensive: %q", f.Detail)
			}
		}
	}
	if !found {
		t.Errorf("a public parameter must be flagged; findings were %+v", Lint(c))
	}
}

func TestLintIgnoresInternalParameters(t *testing.T) {
	c := contractWith(t, `{"audience":"public","reason_code":"TOO_HEAVY",
	  "message":"Too heavy.",
	  "parameters":{"MaxKg":{"as":"limit","audience":"internal"}}}`)
	for _, f := range Lint(c) {
		if strings.Contains(f.Problem, "parameter") {
			t.Errorf("an internal parameter is the correct configuration, not a finding: %+v", f)
		}
	}
}

func TestOracleNamesPublishedParameters(t *testing.T) {
	// Declared provenance, not a wordlist: this parameter is published as
	// "ceiling_value", which matches no boundary word.
	c := contractWith(t, `{"audience":"public","reason_code":"TOO_HEAVY",
	  "message":"Declined.",
	  "parameters":{"MaxKg":{"as":"ceiling_value","audience":"public"}}}`)
	a := AnalyseOracle(c, Public)
	var found bool
	for _, o := range a.Observations {
		if strings.Contains(o.What, "policy parameter") {
			found = true
		}
	}
	if !found {
		t.Errorf("a published parameter should be an oracle observation regardless of its name: %+v", a.Observations)
	}
}
