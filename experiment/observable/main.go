// Command observable closes the gap experiment 4 left open.
//
// Experiment 4 found that disclosure's cost is concentrated in attested
// dimensions — quantities the requester cannot set — and that the gap between
// full and outcome-only disclosure runs to 7x as inputs move out of reach.
//
// It assumed the requester neither sets nor sees those values. That is right
// for an internal risk tier or a carrier margin and wrong for a bureau score,
// which an applicant can pull themselves. An adversary who observes an attested
// value cannot binary-search it, but they can wait: submit repeatedly, record
// the value that happened to arrive with each application, and bracket the
// threshold between the largest approving observation and the smallest
// declining one.
//
// So the question is whether observability collapses the 7x, and the answer
// determines what you actually tell a client:
//
//	if it collapses    "your notice must not print internal quantities"
//	if it does not     "your notice must not print quantities the requester
//	                    cannot observe" — a different and narrower rule
package main

import (
	"fmt"
	"math/rand"
	"os"
	"strings"
)

const (
	nFeatures = 12
	scale     = 4096
	// tolerance for calling a threshold recovered. Passive observation
	// brackets rather than pinpoints, so exact equality would understate a
	// real adversary: knowing a cutoff to within 1% is knowing it.
	tolerance = scale / 100
)

type rule struct {
	feature int
	thresh  int
	above   bool
}

type world struct {
	rules    []rule
	attested map[int]bool
	queries  int
	rng      *rand.Rand
}

// submit runs one application. The returned vector is what the adversary
// observes of the attested features — nil when they are invisible.
func (w *world) submit(sub map[int]int, observable bool) (declined bool, fired int, seen map[int]int) {
	w.queries++
	var x [nFeatures]int
	seen = map[int]int{}
	for f := 0; f < nFeatures; f++ {
		if w.attested[f] {
			x[f] = w.rng.Intn(scale)
			if observable {
				seen[f] = x[f]
			}
			continue
		}
		if v, ok := sub[f]; ok {
			x[f] = v
		} else {
			x[f] = scale / 2
		}
	}
	for i, r := range w.rules {
		if (r.above && x[r.feature] > r.thresh) || (!r.above && x[r.feature] < r.thresh) {
			return true, i, seen
		}
	}
	return false, -1, seen
}

type policy int

const (
	full policy = iota
	reason
	outcome
)

func (p policy) String() string { return [...]string{"full", "reason", "outcome"}[p] }

// bracket accumulates observations of one attested feature and reports how
// tightly the threshold is pinned.
type bracket struct {
	loApprove, hiApprove int
	loDecline, hiDecline int
	seenA, seenD         bool
}

func (b *bracket) add(v int, declined bool) {
	if declined {
		if !b.seenD || v < b.loDecline {
			b.loDecline = v
		}
		if !b.seenD || v > b.hiDecline {
			b.hiDecline = v
		}
		b.seenD = true
		return
	}
	if !b.seenA || v < b.loApprove {
		b.loApprove = v
	}
	if !b.seenA || v > b.hiApprove {
		b.hiApprove = v
	}
	b.seenA = true
}

// resolved reports the tightest gap between an approving and a declining
// observation, which is how well the threshold is known.
func (b *bracket) resolved(above bool) (int, bool) {
	if !b.seenA || !b.seenD {
		return 0, false
	}
	if above {
		// declines are high: threshold sits between hiApprove and loDecline
		if b.loDecline-b.hiApprove <= tolerance && b.loDecline > b.hiApprove {
			return (b.loDecline + b.hiApprove) / 2, true
		}
		return 0, false
	}
	if b.hiDecline-b.loApprove <= tolerance && b.loApprove > b.hiDecline {
		return (b.loApprove + b.hiDecline) / 2, true
	}
	return 0, false
}

// attack recovers what it can within a query budget.
func attack(w *world, p policy, observable bool, budget int) float64 {
	recovered := map[int]bool{}
	base := map[int]int{}

	// First, the submitted features: same peel-off search as before, cheap.
	for f := 0; f < nFeatures && w.queries < budget/2; f++ {
		if w.attested[f] {
			continue
		}
		for _, v := range []int{0, scale} {
			probe := clone(base)
			probe[f] = v
			declined, fired, _ := w.submit(probe, observable)
			if !declined || recovered[fired] {
				continue
			}
			r := w.rules[fired]
			if p == full || r.feature == f {
				recovered[fired] = true
				if !w.attested[r.feature] {
					if r.above {
						base[r.feature] = 0
					} else {
						base[r.feature] = scale
					}
				}
			}
		}
	}

	// Then the attested ones. Under full disclosure the notice prints them.
	// Otherwise the only route is observation, and only if they are visible.
	brackets := map[int]*bracket{}
	for w.queries < budget {
		declined, fired, seen := w.submit(base, observable)
		if declined && p == full {
			recovered[fired] = true
		}
		if !observable {
			if !declined {
				continue
			}
			continue
		}
		for f, v := range seen {
			if brackets[f] == nil {
				brackets[f] = &bracket{}
			}
			// A decline is only evidence about feature f if f is what fired.
			// With reason disclosure the adversary is told; with outcome only
			// they must attribute, and a decline from another attested rule is
			// noise they cannot separate.
			// An approval is evidence about every attested feature at once:
			// all of them passed. A decline is evidence about exactly one, and
			// which one is the thing disclosure does or does not supply.
			if !declined {
				brackets[f].add(v, false)
				continue
			}
			switch p {
			case reason, full:
				// The notice names the feature, so the observation lands where
				// it belongs.
				if w.rules[fired].feature == f {
					brackets[f].add(v, true)
				}
			default:
				// Outcome only. The adversary knows an attested rule fired but
				// not which, so the observation has to be charged to all of
				// them — and the ones it does not belong to are poisoned by it.
				brackets[f].add(v, true)
			}
		}
	}
	for i, r := range w.rules {
		if recovered[i] || !w.attested[r.feature] {
			continue
		}
		if b := brackets[r.feature]; b != nil {
			if _, ok := b.resolved(r.above); ok {
				recovered[i] = true
			}
		}
	}
	return float64(len(recovered)) / float64(len(w.rules))
}

func clone(m map[int]int) map[int]int {
	o := make(map[int]int, len(m))
	for k, v := range m {
		o[k] = v
	}
	return o
}

func build(nRules int, frac float64, rng *rand.Rand) *world {
	att := map[int]bool{}
	perm := rng.Perm(nFeatures)
	for i := 0; i < int(frac*nFeatures); i++ {
		att[perm[i]] = true
	}
	rp := rng.Perm(nFeatures)
	rules := make([]rule, 0, nRules)
	for i := 0; i < nRules; i++ {
		rules = append(rules, rule{rp[i%nFeatures], scale/4 + rng.Intn(scale/2), rng.Intn(2) == 0})
	}
	return &world{rules: rules, attested: att, rng: rand.New(rand.NewSource(5))}
}

func main() {
	const nRules, trials, frac = 8, 40, 0.5
	var b strings.Builder
	fmt.Fprintf(&b, "Observable but not settable\n")
	fmt.Fprintf(&b, "%d rules over %d features, %.0f%% attested. Threshold counted recovered\n",
		nRules, nFeatures, frac*100)
	fmt.Fprintf(&b, "within %.0f%% of scale. %d trials per cell. Figures are rules recovered.\n\n", 100.0/100, trials)
	fmt.Fprintf(&b, "  %8s  %-28s %-28s\n", "budget", "attested values HIDDEN", "attested values OBSERVED")
	fmt.Fprintf(&b, "  %8s  %8s%10s%10s %8s%10s%10s\n", "", "full", "reason", "outcome", "full", "reason", "outcome")

	for _, budget := range []int{200, 1000, 5000, 20000, 100000} {
		fmt.Fprintf(&b, "  %8d ", budget)
		for _, obs := range []bool{false, true} {
			for _, p := range []policy{full, reason, outcome} {
				var acc float64
				for t := 0; t < trials; t++ {
					w := build(nRules, frac, rand.New(rand.NewSource(int64(t)+17)))
					acc += attack(w, p, obs, budget)
				}
				fmt.Fprintf(&b, " %8.0f%%", acc/trials*100)
			}
			fmt.Fprint(&b, " ")
		}
		fmt.Fprintln(&b)
	}

	fmt.Fprint(&b, `
Reading it

  Under hidden attestation the columns reproduce experiment 4: an attested
  threshold is not findable at any budget, so the notice is the only route and
  full disclosure is the only policy that supplies one.

  Under observed attestation the adversary can bracket rather than search. That
  converts impossible into expensive — the question is how expensive, and
  whether it stays expensive enough to matter.

  Whatever the answer, note the full column: one query per rule, at every
  budget, under both conditions. Printing a threshold in a notice costs the
  same whether or not the requester could ever have found it themselves. That
  is the asymmetry a disclosure contract exists to manage.
`)
	os.Stdout.WriteString(b.String())
}
