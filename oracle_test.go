package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A contract that leaks no engine vocabulary at all, and is still an oracle.
// This is the case lint cannot see and the reason this analysis exists.
const fraudContract = `{
  "contract_version": "1",
  "ruleset": "payment-fraud",
  "outcomes": {
    "ALLOW":  { "as": "approved", "audience": "public" },
    "DENY":   { "as": "declined", "audience": "public" },
    "REVIEW": { "as": "pending",  "audience": "partner" }
  },
  "rules": {
    "RL_VEL_24H": {
      "audience": "public",
      "reason_code": "VELOCITY_LIMIT_EXCEEDED",
      "message": "Too many attempts in the last {window} hours; the limit is {limit}.",
      "facts": {
        "WindowHours": { "as": "window", "audience": "public" },
        "MaxAttempts": { "as": "limit",  "audience": "public" }
      }
    },
    "RL_GEO_MISMATCH": {
      "audience": "public",
      "reason_code": "DECLINED",
      "message": "We could not approve this payment.",
      "facts": {}
    },
    "RL_SCORE_BAND": {
      "audience": "internal",
      "reason_code": "RISK_SCORE_BAND_3",
      "message": "Score 812 falls in band 3.",
      "facts": {}
    }
  }
}`

func fraud(t *testing.T) *Contract {
	t.Helper()
	p := filepath.Join(t.TempDir(), "fraud.json")
	if err := os.WriteFile(p, []byte(fraudContract), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := LoadContract(p)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func findObs(a *Analysis, where, what string) *Observation {
	for i := range a.Observations {
		if a.Observations[i].Where == where && strings.Contains(a.Observations[i].What, what) {
			return &a.Observations[i]
		}
	}
	return nil
}

// The check lint cannot make: a code naming the mechanism tells an attacker
// which dimension to vary.
func TestMechanismNamingIsObserved(t *testing.T) {
	a := AnalyseOracle(fraud(t), Public)
	if findObs(a, "VELOCITY_LIMIT_EXCEEDED", "names the mechanism") == nil {
		t.Fatalf("VELOCITY_LIMIT_EXCEEDED should be flagged; got %+v", a.Observations)
	}
	// The uninformative code must not be.
	if findObs(a, "DECLINED", "names the mechanism") != nil {
		t.Error(`"DECLINED" says only that it happened and should not be flagged`)
	}
}

func TestPublishedBoundariesAreObserved(t *testing.T) {
	a := AnalyseOracle(fraud(t), Public)
	for _, w := range []string{"window", "limit"} {
		if findObs(a, "VELOCITY_LIMIT_EXCEEDED", `publishes a boundary as "`+w) == nil {
			t.Errorf("publishing %q as a fact should be flagged: %+v", w, a.Observations)
		}
	}
}

func TestHardcodedNumbersInMessagesAreObserved(t *testing.T) {
	a := AnalyseOracle(fraud(t), Internal)
	if findObs(a, "RISK_SCORE_BAND_3", "carries a number") == nil {
		t.Errorf("a literal score in a message should be flagged: %+v", a.Observations)
	}
}

// Audience scoping applies here too: an internal-only reason is not part of the
// public disclosure surface.
func TestInternalReasonsAreNotPublicSurface(t *testing.T) {
	pub := AnalyseOracle(fraud(t), Public)
	if findObs(pub, "RISK_SCORE_BAND_3", "") != nil {
		t.Error("an internal reason must not appear in the public analysis")
	}
	in := AnalyseOracle(fraud(t), Internal)
	if in.Reasons <= pub.Reasons {
		t.Errorf("internal should see more reasons: %d vs %d", in.Reasons, pub.Reasons)
	}
}

// Bandwidth is the point of the count: reasons come back as a set, so each
// additional public reason doubles what one query can distinguish.
func TestBandwidthGrowsWithTheSubsets(t *testing.T) {
	a := AnalyseOracle(fraud(t), Public)
	// 2 public outcomes, 2 public reasons -> 2 * 2^2
	if a.Outcomes != 2 || a.Reasons != 2 || a.States != 8 {
		t.Fatalf("outcomes=%d reasons=%d states=%d, want 2/2/8", a.Outcomes, a.Reasons, a.States)
	}

	in := AnalyseOracle(fraud(t), Internal)
	if in.States != in.Outcomes*(1<<in.Reasons) {
		t.Errorf("states = %d, want %d", in.States, in.Outcomes*(1<<in.Reasons))
	}
	if in.States <= a.States {
		t.Error("a wider audience should distinguish more, not less")
	}
}

func TestNoReasonsMeansOutcomesOnly(t *testing.T) {
	var c Contract
	json.Unmarshal([]byte(`{"contract_version":"1","ruleset":"x",
	  "outcomes":{"A":{"as":"a","audience":"public"}},
	  "rules":{"R":{"audience":"internal","reason_code":"X"}}}`), &c)
	a := AnalyseOracle(&c, Public)
	if a.Reasons != 0 || a.States != 1 {
		t.Errorf("outcomes=%d reasons=%d states=%d", a.Outcomes, a.Reasons, a.States)
	}
}

// The shipping contract is a domain where publishing a limit is correct. The
// analysis should still say so plainly rather than staying silent.
func TestAGoodContractStillHasASurface(t *testing.T) {
	c, err := LoadContract("testdata/contract.json")
	if err != nil {
		t.Fatal(err)
	}
	a := AnalyseOracle(c, Public)
	if a.States < 1 {
		t.Error("every contract has a disclosure surface, including a well-made one")
	}
	if findObs(a, "PACKAGE_TOO_HEAVY", "publishes a boundary") == nil {
		t.Error("publishing the mass limit should be reported, for the reader to accept")
	}
}

func TestReportIsStableAndReadable(t *testing.T) {
	var b strings.Builder
	a := AnalyseOracle(fraud(t), Public)
	sortObservations(a.Observations)
	a.Report(&b)
	out := b.String()

	for _, want := range []string{"public", "distinguishable answers per query", "VELOCITY_LIMIT_EXCEEDED"} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q:\n%s", want, out)
		}
	}
}
