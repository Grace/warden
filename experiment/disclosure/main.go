// Command disclosure measures whether the tradeoff between explanation
// compliance and model-extraction risk is real or architectural.
//
// The field currently accepts a tradeoff. Regulation — ECOA/Reg B adverse
// action notices, EU AI Act Art. 86 — requires specific, accurate, per-person
// reasons for an adverse decision. The security literature shows those same
// explanations let an adversary reconstruct the model in a few hundred queries,
// and the published defence is to add noise to the explanations, which buys
// security by degrading the thing the law requires.
//
// This asks whether the tradeoff survives once you notice that *the parties the
// law entitles to an explanation cannot mount the attack*. A credit applicant
// is one authenticated person, receiving one explanation, about one decision
// concerning themselves. Extraction needs bulk arbitrary queries about
// synthetic subjects. Those are different access patterns, and a system that
// distinguishes them may be able to hold compliance fixed while pushing
// extraction cost to the outcome-only floor.
//
// Two numbers per policy, and both matter:
//
//	extraction  — surrogate agreement with the target after a query budget
//	compliance  — whether an entitled subject's disclosed principal reasons
//	              match the true ones, which is what Reg B actually demands
//
// A defence that wins on one and loses on the other has not solved anything.
package main

import (
	"fmt"
	"math"
	"math/rand"
	"os"
	"sort"
	"strings"
)

// ---------- the target ----------

const nFeatures = 6

var featureNames = [nFeatures]string{
	"utilization", "delinquencies", "inquiries", "tenure", "dti", "thin_file",
}

// model is a linear scorer with a decision threshold — the shape of a credit
// model simple enough that extraction success is unambiguous.
type model struct {
	w [nFeatures]float64
	b float64
}

func target() *model {
	return &model{
		w: [nFeatures]float64{-1.8, -2.4, -0.7, 1.1, -1.3, -0.9},
		b: 0.35,
	}
}

func (m *model) score(x [nFeatures]float64) float64 {
	s := m.b
	for i := range x {
		s += m.w[i] * x[i]
	}
	return s
}

func (m *model) approve(x [nFeatures]float64) bool { return m.score(x) >= 0 }

// contributions is what a faithful explanation reports: per-feature signed
// contribution to this decision. Reg B's "principal reasons" are the largest
// adverse ones.
func (m *model) contributions(x [nFeatures]float64) [nFeatures]float64 {
	var c [nFeatures]float64
	for i := range x {
		c[i] = m.w[i] * x[i]
	}
	return c
}

// principalReasons is the top-k adverse contributions, as an adverse action
// notice would list them.
func principalReasons(c [nFeatures]float64, k int) []int {
	idx := make([]int, nFeatures)
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return c[idx[a]] < c[idx[b]] })
	return idx[:k]
}

// ---------- disclosure policies ----------

type policy struct {
	name string
	// entitled is what an authenticated data subject receives about their own
	// decision. nil contributions means outcome only.
	entitled func(m *model, x [nFeatures]float64, rng *rand.Rand) ([nFeatures]float64, bool)
	// bulk is what an unauthenticated or high-volume requester receives.
	bulk func(m *model, x [nFeatures]float64, rng *rand.Rand) ([nFeatures]float64, bool)
	// identityCost is how many distinct identities the adversary must
	// manufacture to obtain one explanation. Zero means explanations are
	// available without one.
	identityCost int
	note         string
}

func noisy(eps float64) func(*model, [nFeatures]float64, *rand.Rand) ([nFeatures]float64, bool) {
	return func(m *model, x [nFeatures]float64, rng *rand.Rand) ([nFeatures]float64, bool) {
		c := m.contributions(x)
		for i := range c {
			c[i] += rng.NormFloat64() * eps
		}
		return c, true
	}
}

func faithful(m *model, x [nFeatures]float64, _ *rand.Rand) ([nFeatures]float64, bool) {
	return m.contributions(x), true
}

func outcomeOnly(*model, [nFeatures]float64, *rand.Rand) ([nFeatures]float64, bool) {
	return [nFeatures]float64{}, false
}

func policies() []policy {
	return []policy{
		{"uniform full disclosure", faithful, faithful, 0,
			"every requester gets the explanation the applicant is owed"},
		{"DP-noised (eps 0.5)", noisy(0.5), noisy(0.5), 0,
			"the published defence: perturb explanations for everyone"},
		{"DP-noised (eps 1.5)", noisy(1.5), noisy(1.5), 0,
			"more noise, less leakage, worse notices"},
		{"outcome only", outcomeOnly, outcomeOnly, 0,
			"maximally safe, and unlawful under Reg B"},
		{"entitlement-scoped", faithful, outcomeOnly, 1,
			"subject gets a faithful notice; bulk requesters get an outcome"},
	}
}

// ---------- the adversary ----------

// extract builds a surrogate under a policy and returns its agreement with the
// target on held-out inputs.
//
// The adversary is assumed competent and to know the model family. When
// contributions are available they solve for the weights directly, which is
// the efficient attack the literature describes. When only outcomes are
// available they must probe the decision boundary, which is far more
// expensive and is what makes the identity cost bite.
func extract(m *model, p policy, budget int, rng *rand.Rand) float64 {
	var sur model
	obs := 0

	if _, ok := p.bulk(m, [nFeatures]float64{}, rng); ok || p.identityCost > 0 {
		// Contributions are reachable — either freely, or by manufacturing an
		// identity per explanation. Recover each weight from c_i = w_i * x_i.
		var sum [nFeatures]float64
		var n [nFeatures]float64
		queries := budget
		if p.identityCost > 0 {
			queries = budget / p.identityCost
		}
		for q := 0; q < queries; q++ {
			x := randomApplicant(rng)
			get := p.bulk
			if p.identityCost > 0 {
				get = p.entitled // paid for with a manufactured identity
			}
			c, ok := get(m, x, rng)
			if !ok {
				continue
			}
			obs++
			for i := range x {
				if math.Abs(x[i]) > 0.2 {
					sum[i] += c[i] / x[i]
					n[i]++
				}
			}
		}
		for i := range sur.w {
			if n[i] > 0 {
				sur.w[i] = sum[i] / n[i]
			}
		}
		sur.b = recoverBias(m, &sur, p, rng, 40)
	}

	if obs == 0 {
		// Outcome-only: probe the boundary. Each query yields one bit, so this
		// is the expensive path by construction.
		sur = boundaryProbe(m, budget, rng)
	}
	return agreement(m, &sur, rng)
}

// recoverBias finds the intercept once the weights are known, by bisecting on
// a single feature until the decision flips.
func recoverBias(m *model, sur *model, p policy, rng *rand.Rand, budget int) float64 {
	lo, hi := -5.0, 5.0
	// approve() is monotone in x[0]; which direction depends on the sign of the
	// weight, and getting this backwards silently produces exact weights with a
	// nonsense intercept — a surrogate that looks recovered and decides at
	// chance.
	up := m.approve([nFeatures]float64{hi})
	for i := 0; i < budget; i++ {
		mid := (lo + hi) / 2
		if m.approve([nFeatures]float64{mid}) == up {
			hi = mid
		} else {
			lo = mid
		}
	}
	cross := (lo + hi) / 2
	return -sur.w[0] * cross
}

// boundaryProbe fits a surrogate from accept/reject alone, by perceptron
// updates on random applicants. One bit per query.
func boundaryProbe(m *model, budget int, rng *rand.Rand) model {
	var sur model
	for q := 0; q < budget; q++ {
		x := randomApplicant(rng)
		want, got := m.approve(x), sur.approve(x)
		if want == got {
			continue
		}
		sign := -1.0
		if want {
			sign = 1.0
		}
		for i := range x {
			sur.w[i] += 0.05 * sign * x[i]
		}
		sur.b += 0.05 * sign
	}
	return sur
}

func randomApplicant(rng *rand.Rand) [nFeatures]float64 {
	var x [nFeatures]float64
	for i := range x {
		x[i] = rng.NormFloat64()
	}
	return x
}

// agreement is surrogate fidelity: how often it decides as the target does.
func agreement(m, sur *model, rng *rand.Rand) float64 {
	const n = 20000
	hit := 0
	for i := 0; i < n; i++ {
		x := randomApplicant(rng)
		if m.approve(x) == sur.approve(x) {
			hit++
		}
	}
	return float64(hit) / n
}

// ---------- compliance ----------

// complianceRate is the fraction of denied applicants whose disclosed
// principal reasons match the true ones. This is the Reg B obligation stated
// as a measurement: a notice naming the wrong factors does not satisfy it.
func complianceRate(m *model, p policy, rng *rand.Rand) float64 {
	const trials, k = 3000, 3
	ok, seen := 0, 0
	for i := 0; i < trials; i++ {
		x := randomApplicant(rng)
		if m.approve(x) {
			continue
		}
		seen++
		truth := principalReasons(m.contributions(x), k)
		c, disclosed := p.entitled(m, x, rng)
		if !disclosed {
			continue // an outcome is not a statement of reasons
		}
		got := principalReasons(c, k)
		if sameSet(truth, got) {
			ok++
		}
	}
	if seen == 0 {
		return 0
	}
	return float64(ok) / float64(seen)
}

func sameSet(a, b []int) bool {
	m := map[int]bool{}
	for _, v := range a {
		m[v] = true
	}
	for _, v := range b {
		if !m[v] {
			return false
		}
	}
	return len(a) == len(b)
}

func main() {
	m := target()
	const budget = 600 // the literature's ~500-query surrogate, rounded up

	var b strings.Builder
	fmt.Fprintf(&b, "Explanation disclosure: compliance against extraction\n")
	fmt.Fprintf(&b, "Target: linear credit model, %d features. Adversary budget: %d queries.\n", nFeatures, budget)
	fmt.Fprintf(&b, "Compliance = share of denials whose top-3 principal reasons are disclosed correctly.\n")
	fmt.Fprintf(&b, "Extraction = surrogate agreement with the target on held-out applicants.\n\n")
	fmt.Fprintf(&b, "  %-26s %11s %11s   %s\n", "policy", "compliance", "extraction", "")
	for _, p := range policies() {
		rng := rand.New(rand.NewSource(1))
		comp := complianceRate(m, p, rng)
		ext := extract(m, p, budget, rand.New(rand.NewSource(2)))
		fmt.Fprintf(&b, "  %-26s %10.0f%% %10.0f%%   %s\n", p.name, comp*100, ext*100, p.note)
	}

	fmt.Fprint(&b, `
Manufacturing entitlement is the security parameter
`)
	fmt.Fprintf(&b, "  Under entitlement-scoped disclosure an adversary must present a distinct\n")
	fmt.Fprintf(&b, "  authenticated subject per explanation. Extraction quality against the cost\n")
	fmt.Fprintf(&b, "  of doing so:\n\n")
	fmt.Fprintf(&b, "  %-24s %s\n", "identities manufactured", "extraction")
	for _, ids := range []int{10, 50, 200, 600} {
		p := policy{"scoped", faithful, outcomeOnly, 1, ""}
		ext := extract(m, p, ids, rand.New(rand.NewSource(3)))
		fmt.Fprintf(&b, "  %-24d %9.0f%%\n", ids, ext*100)
	}
	os.Stdout.WriteString(b.String())
}
