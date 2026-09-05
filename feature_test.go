package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFeature(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "x.feature")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func contract(t *testing.T) *Contract {
	t.Helper()
	c, err := LoadContract("testdata/contract.json")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func runOne(t *testing.T, body string) (string, int, int) {
	t.Helper()
	var buf bytes.Buffer
	p, f, err := RunFeatures(&buf, contract(t), []string{writeFeature(t, body)})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return buf.String(), p, f
}

const passing = `Feature: f
  Scenario: a heavy package
    Given the ruleset "shipping-eligibility"
    And the outcome "DENY"
    And rule "RL_PKG_MASS_OVER_LIMIT" fires with:
      | PkgMassKg | 34.5 |
      | MaxMassKg | 30   |
    When published to "public"
    Then the outcome is "not_eligible"
    And reason "PACKAGE_TOO_HEAVY" is present
    And the message for "PACKAGE_TOO_HEAVY" is "This package is 34.5 kg; the limit is 30 kg."
`

func TestAScenarioRunsAgainstTheRealProjection(t *testing.T) {
	out, pass, fail := runOne(t, passing)
	if pass != 1 || fail != 0 {
		t.Fatalf("passed=%d failed=%d\n%s", pass, fail, out)
	}
}

// Someone who cannot read the ruleset is working out what it does. Three
// findings tell them more than one, so every assertion runs.
func TestEveryAssertionRunsRatherThanStoppingAtTheFirst(t *testing.T) {
	out, _, fail := runOne(t, `Feature: f
  Scenario: three things wrong
    Given the ruleset "shipping-eligibility"
    And the outcome "DENY"
    And rule "RL_PKG_MASS_OVER_LIMIT" fires with:
      | PkgMassKg | 34.5 |
      | MaxMassKg | 30   |
    When published to "public"
    Then the outcome is "declined"
    And reason "NOPE" is present
    And there are 9 reasons
`)
	if fail != 1 {
		t.Fatalf("failed=%d\n%s", fail, out)
	}
	for _, want := range []string{`expected "declined"`, `no reason "NOPE"`, "expected 9"} {
		if !strings.Contains(out, want) {
			t.Errorf("report should contain %q:\n%s", want, out)
		}
	}
}

// A failure has to say what is actually there, not only that expectations were
// not met, because the reader is trying to learn the contract.
func TestFailuresNameWhatIsActuallyPublished(t *testing.T) {
	out, _, _ := runOne(t, `Feature: f
  Scenario: wrong fact name
    Given the ruleset "shipping-eligibility"
    And the outcome "DENY"
    And rule "RL_PKG_MASS_OVER_LIMIT" fires with:
      | PkgMassKg | 34.5 |
      | MaxMassKg | 30   |
    When published to "public"
    Then reason "PACKAGE_TOO_HEAVY" has fact "weight" as "34.5"
`)
	if !strings.Contains(out, `it has "limit", "mass"`) {
		t.Errorf("should list the facts that do exist:\n%s", out)
	}
}

// Deny-by-default is behaviour worth asserting, not only an error path.
func TestRefusalCanBeAsserted(t *testing.T) {
	out, pass, fail := runOne(t, `Feature: f
  Scenario: an unmapped rule
    Given the ruleset "shipping-eligibility"
    And the outcome "DENY"
    And rule "RL_BRAND_NEW" fires
    When published to "public"
    Then publishing is refused
    And publishing is refused because "not in the contract"
`)
	if pass != 1 || fail != 0 {
		t.Fatalf("passed=%d failed=%d\n%s", pass, fail, out)
	}
}

func TestExpectingARefusalThatDoesNotHappenFails(t *testing.T) {
	out, _, fail := runOne(t, `Feature: f
  Scenario: expects refusal wrongly
    Given the ruleset "shipping-eligibility"
    And the outcome "DENY"
    And rule "RL_PKG_MASS_OVER_LIMIT" fires with:
      | PkgMassKg | 34.5 |
      | MaxMassKg | 30   |
    When published to "public"
    Then publishing is refused
`)
	if fail != 1 || !strings.Contains(out, "expected a refusal") {
		t.Errorf("failed=%d\n%s", fail, out)
	}
}

// A message with a hole in it must never be published, so a rule firing without
// the facts its message needs is refused rather than rendered with the
// placeholder still in it.
func TestMissingFactsRefusePublication(t *testing.T) {
	out, pass, fail := runOne(t, `Feature: f
  Scenario: the engine sent the rule but not its facts
    Given the ruleset "shipping-eligibility"
    And the outcome "DENY"
    And rule "RL_PKG_MASS_OVER_LIMIT" fires
    When published to "public"
    Then publishing is refused
`)
	if pass != 1 || fail != 0 {
		t.Fatalf("passed=%d failed=%d\n%s", pass, fail, out)
	}
}

// A scenario that asserts nothing is worse than no scenario: it reads as
// coverage and is not.
func TestScenarioWithNoAssertionsIsAFailure(t *testing.T) {
	out, _, fail := runOne(t, `Feature: f
  Scenario: sets up and checks nothing
    Given the ruleset "shipping-eligibility"
    And the outcome "DENY"
    When published to "public"
`)
	if fail != 1 || !strings.Contains(out, "asserts nothing") {
		t.Errorf("failed=%d\n%s", fail, out)
	}
}

// The vocabulary is closed. A step nobody implemented must not look like it ran.
func TestUnknownStepsAreRefusedAtParse(t *testing.T) {
	cases := map[string]string{
		"unknown step": `Feature: f
  Scenario: s
    Given the moon is full
`,
		"step with no keyword": `Feature: f
  Scenario: s
    the ruleset "x"
`,
		"table with no rule": `Feature: f
  Scenario: s
    Given the ruleset "shipping-eligibility"
      | a | b |
`,
		"step before any scenario": `Feature: f
  Given the ruleset "x"
`,
		"unknown audience": `Feature: f
  Scenario: s
    Given the ruleset "shipping-eligibility"
    When published to "everyone"
`,
	}
	for name, body := range cases {
		if _, err := ParseFeature(writeFeature(t, body)); err == nil {
			t.Errorf("%s: should be refused", name)
		}
	}
}

func TestParseKeepsFeatureAndScenarioNames(t *testing.T) {
	f, err := ParseFeature(writeFeature(t, passing))
	if err != nil {
		t.Fatal(err)
	}
	if f.Name != "f" {
		t.Errorf("feature name = %q", f.Name)
	}
	if len(f.Scenarios) != 1 || f.Scenarios[0].Name != "a heavy package" {
		t.Fatalf("scenarios = %+v", f.Scenarios)
	}
}

// Table cells keep their type, so a scenario reads naturally and the projection
// sees what the engine would actually have sent.
func TestTableCellsKeepTheirType(t *testing.T) {
	for in, want := range map[string]string{
		"34.5":  "34.5",
		"30":    "30",
		"true":  "true",
		"Z1-Z4": `"Z1-Z4"`,
		"":      `""`,
	} {
		if got := string(jsonScalar(in)); got != want {
			t.Errorf("jsonScalar(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestCommentsAndBlankLinesAreIgnored(t *testing.T) {
	out, pass, fail := runOne(t, "# a note\n\n"+passing+"\n# trailing\n")
	if pass != 1 || fail != 0 {
		t.Fatalf("passed=%d failed=%d\n%s", pass, fail, out)
	}
}

// The shipped feature file is the documentation, so it has to pass.
func TestShippedFeaturesPass(t *testing.T) {
	matches, _ := filepath.Glob("features/*.feature")
	if len(matches) == 0 {
		t.Skip("no feature files")
	}
	var buf bytes.Buffer
	_, fail, err := RunFeatures(&buf, contract(t), matches)
	if err != nil {
		t.Fatal(err)
	}
	if fail != 0 {
		t.Errorf("shipped features failing:\n%s", buf.String())
	}
}
