// Command scaling asks the question the first experiment could not answer:
// does the cost of extracting a decision system depend on what you disclose,
// and does that gap widen with the system's complexity?
//
// The earlier run used a six-parameter linear model and found that nothing
// helped — outcomes alone gave up the model in a few hundred queries, so
// withholding explanations bought nothing. That is a real result about simple
// models and a bad basis for a general claim.
//
// The target here is a knockout rule list over a scorecard, which is what
// underwriting actually looks like: a sequence of "reject if this feature
// crosses this threshold" rules, evaluated in order. Complexity is the number
// of rules, and it is the variable being swept.
//
// The adversary's goal is full recovery: every rule's feature *and* threshold.
// Cost is queries. Three disclosure policies:
//
//	full     the notice names the rule and prints its threshold
//	reason   the notice names the feature but not the threshold
//	outcome  approved or declined, nothing more
//
// The ratio between outcome and full is the protection that scoping explanation
// can buy, and it is the number the entitlement framework lives or dies on:
// manufacturing identities is only a meaningful defence if it costs more than
// that ratio multiplies.
package main

import (
	"fmt"
	"math/rand"
	"os"
	"strings"
)

const nFeatures = 12

// rule rejects when the feature crosses the threshold in the stated direction.
type rule struct {
	feature int
	thresh  int // integer scale 0..scale
	above   bool
}

const scale = 4096

type engine struct {
	rules   []rule
	queries int
}

// applicant is the adversary's input.
type applicant [nFeatures]int

// verdict is the engine's internal decision before any boundary.
type verdict struct {
	declined bool
	fired    int // index into rules, -1 if approved
}

func (e *engine) decide(a applicant) verdict {
	e.queries++
	for i, r := range e.rules {
		v := a[r.feature]
		if (r.above && v > r.thresh) || (!r.above && v < r.thresh) {
			return verdict{true, i}
		}
	}
	return verdict{false, -1}
}

// ---------- disclosure ----------

type policy int

const (
	full policy = iota
	reason
	outcome
)

func (p policy) String() string {
	return [...]string{"full: rule and threshold", "reason: feature named", "outcome only"}[p]
}

// notice is what the requester sees.
type notice struct {
	declined bool
	feature  int // -1 when not disclosed
	thresh   int // -1 when not disclosed
}

func project(e *engine, v verdict, p policy) notice {
	if !v.declined {
		return notice{false, -1, -1}
	}
	r := e.rules[v.fired]
	switch p {
	case full:
		return notice{true, r.feature, r.thresh}
	case reason:
		return notice{true, r.feature, -1}
	default:
		return notice{true, -1, -1}
	}
}

// ---------- the adversary ----------

// benign is an applicant that passes every rule, which the adversary can find
// cheaply and then perturb one feature at a time. Constructing it is charged.
func (e *engine) benign(rng *rand.Rand) (applicant, bool) {
	var a applicant
	for i := range a {
		a[i] = scale / 2
	}
	for tries := 0; tries < 200; tries++ {
		v := e.decide(a)
		if !v.declined {
			return a, true
		}
		// Nudge the offending feature away from its threshold. Under outcome
		// disclosure the adversary does not know which feature, so it perturbs
		// all of them — that cost is real and stays charged.
		r := e.rules[v.fired]
		if r.above {
			a[r.feature] = 0
		} else {
			a[r.feature] = scale
		}
	}
	return a, false
}

// recover finds every rule's feature and threshold, returning the query cost.
// It returns -1 if it fails to recover them all inside the cap.
func recover(rules []rule, p policy, rng *rand.Rand) int {
	e := &engine{rules: rules}
	base, ok := e.benign(rng)
	if !ok {
		return -1
	}
	found := make(map[int]bool) // rules recovered, by index

	// Rules are shadowed by earlier ones, so the adversary peels them off: find
	// the rule that fires, learn it, then hold that feature safe and repeat.
	safe := base
	for len(found) < len(rules) {
		// Drive every unresolved feature to an extreme to make some rule fire.
		probe := safe
		fired := -1
		for f := 0; f < nFeatures; f++ {
			probe = safe
			probe[f] = 0
			if v := e.decide(probe); v.declined && !found[v.fired] {
				fired = v.fired
				break
			}
			probe = safe
			probe[f] = scale
			if v := e.decide(probe); v.declined && !found[v.fired] {
				fired = v.fired
				break
			}
		}
		if fired < 0 {
			break // nothing else reachable
		}

		n := project(e, e.decide(probe), p)
		switch {
		case n.thresh >= 0:
			// Handed over. One query, already counted.
		case n.feature >= 0:
			// Dimension named; bisect for the threshold.
			bisect(e, safe, n.feature, p)
		default:
			// Nothing named. Find which feature moved the decision, then bisect.
			f := findFeature(e, safe, probe)
			if f >= 0 {
				bisect(e, safe, f, p)
			}
		}
		found[fired] = true
		// Hold the recovered rule satisfied so the next one becomes reachable.
		r := rules[fired]
		if r.above {
			safe[r.feature] = 0
		} else {
			safe[r.feature] = scale
		}
		if e.queries > 2_000_000 {
			return -1
		}
	}
	if len(found) < len(rules) {
		return -1
	}
	return e.queries
}

// findFeature identifies which feature flipped the decision, by restoring one
// at a time. This is the cost outcome-only disclosure imposes and reason codes
// remove.
func findFeature(e *engine, safe, probe applicant) int {
	for f := 0; f < nFeatures; f++ {
		if safe[f] == probe[f] {
			continue
		}
		test := probe
		test[f] = safe[f]
		if !e.decide(test).declined {
			return f
		}
	}
	return -1
}

// bisect locates a threshold on a known feature using accept/reject alone.
func bisect(e *engine, safe applicant, f int, p policy) {
	lo, hi := 0, scale
	probe := safe
	probe[f] = hi
	up := e.decide(probe).declined
	for lo < hi {
		mid := (lo + hi) / 2
		probe = safe
		probe[f] = mid
		if e.decide(probe).declined == up {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
}

func randomRules(k int, rng *rand.Rand) []rule {
	perm := rng.Perm(nFeatures)
	rs := make([]rule, 0, k)
	for i := 0; i < k; i++ {
		rs = append(rs, rule{
			feature: perm[i%nFeatures],
			thresh:  scale/4 + rng.Intn(scale/2),
			above:   rng.Intn(2) == 0,
		})
	}
	return rs
}

func main() {
	var b strings.Builder
	fmt.Fprintf(&b, "Extraction cost against disclosure and complexity\n")
	fmt.Fprintf(&b, "Target: knockout rule list over %d features, thresholds on a 0..%d scale.\n", nFeatures, scale)
	fmt.Fprintf(&b, "Adversary goal: recover every rule's feature and threshold. Cost: queries.\n")
	fmt.Fprintf(&b, "Median of 25 random rule lists per cell.\n\n")
	fmt.Fprintf(&b, "  %6s %12s %12s %12s %12s\n", "rules", "full", "reason", "outcome", "outcome/full")

	for _, k := range []int{1, 2, 4, 8, 12} {
		var cost [3][]int
		for trial := 0; trial < 25; trial++ {
			rng := rand.New(rand.NewSource(int64(1000*k + trial)))
			rules := randomRules(k, rng)
			for _, p := range []policy{full, reason, outcome} {
				if c := recover(rules, p, rand.New(rand.NewSource(7))); c > 0 {
					cost[p] = append(cost[p], c)
				}
			}
		}
		f, r, o := median(cost[full]), median(cost[reason]), median(cost[outcome])
		ratio := "—"
		if f > 0 {
			ratio = fmt.Sprintf("%.1fx", float64(o)/float64(f))
		}
		fmt.Fprintf(&b, "  %6d %12d %12d %12d %12s\n", k, f, r, o, ratio)
	}

	fmt.Fprint(&b, `
What this measures

  The ratio in the last column is the entire question. It is how much more it
  costs an adversary to reconstruct the decision system when the boundary
  publishes an outcome rather than a reason — and therefore the factor that
  manufacturing an authenticated identity has to beat before entitlement-scoped
  disclosure is a control rather than a decoration.

  If the ratio is flat and small, the earlier finding generalises: explanations
  are not the binding leak and scoping them is theatre. If it grows with rule
  count, there is a regime where scoping is the cheapest control available, and
  the framework is about naming that regime rather than claiming a general win.
`)
	os.Stdout.WriteString(b.String())
}

func median(xs []int) int {
	if len(xs) == 0 {
		return 0
	}
	c := append([]int(nil), xs...)
	for i := 1; i < len(c); i++ {
		for j := i; j > 0 && c[j] < c[j-1]; j-- {
			c[j], c[j-1] = c[j-1], c[j]
		}
	}
	return c[len(c)/2]
}
