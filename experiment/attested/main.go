// Command attested tests the one finding that survived the first three
// experiments: that disclosure protects dimensions the adversary cannot reach
// through the inputs, and only those.
//
// Experiments 1-3 found explanation disclosure worth a constant factor of about
// two against an adversary who controls every input. Experiment 1 also found
// one rule that no adversary recovered under any policy — a threshold on
// carrier margin, a *derived* value the submitter could not set — which the
// engine nonetheless printed in the notice under raw disclosure.
//
// That suggests the leak is not explanation as such but explanation of
// quantities the requester could not have computed themselves. Real decisions
// are full of these: bureau scores, internal risk tiers, fraud model outputs,
// margin floors. The submitter influences none of them directly and observes
// none of them at all.
//
// So: sweep the fraction of features that are attested rather than submitted,
// and measure what each disclosure policy gives away.
//
// Two metrics, because they come apart:
//
//	recovered   the adversary knows the rule's feature *and* its threshold
//	discovered  the adversary knows the rule exists on that feature
//
// An adversary who has discovered a dimension can lobby, social-engineer, or
// buy data to attack it later. One who has not does not know it is there.
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
	maxQuery  = 400_000
)

type rule struct {
	feature int
	thresh  int
	above   bool
}

// world holds which features the adversary may set. Attested features are
// supplied per-application by something outside the adversary's reach and are
// never shown to them — a bureau score, an internal tier, a computed margin.
type world struct {
	rules      []rule
	attested   map[int]bool
	queries    int
	attestRand *rand.Rand
}

func (w *world) decide(submitted map[int]int) (declined bool, fired int) {
	w.queries++
	var x [nFeatures]int
	for f := 0; f < nFeatures; f++ {
		if w.attested[f] {
			x[f] = w.attestRand.Intn(scale)
			continue
		}
		if v, ok := submitted[f]; ok {
			x[f] = v
		} else {
			x[f] = scale / 2
		}
	}
	for i, r := range w.rules {
		v := x[r.feature]
		if (r.above && v > r.thresh) || (!r.above && v < r.thresh) {
			return true, i
		}
	}
	return false, -1
}

type policy int

const (
	full policy = iota
	reason
	outcome
)

func (p policy) String() string {
	return [...]string{"full", "reason", "outcome"}[p]
}

type knowledge struct {
	recovered  map[int]bool
	discovered map[int]bool
}

// attack runs the adversary. It submits what it can, reads what the policy
// discloses, and bisects any controllable dimension it learns about.
func attack(w *world, p policy, budget int) knowledge {
	k := knowledge{map[int]bool{}, map[int]bool{}}
	base := map[int]int{}

	for w.queries < budget && len(k.recovered) < len(w.rules) {
		// Sweep controllable features to extremes to make some rule fire.
		progressed := false
		for f := 0; f < nFeatures && w.queries < budget; f++ {
			if w.attested[f] {
				continue
			}
			for _, v := range []int{0, scale} {
				probe := clone(base)
				probe[f] = v
				declined, fired := w.decide(probe)
				if !declined || k.recovered[fired] {
					continue
				}
				r := w.rules[fired]

				switch p {
				case full:
					// The notice names the rule and prints its threshold,
					// whether or not the requester could have found it.
					k.discovered[fired], k.recovered[fired] = true, true
				case reason:
					k.discovered[fired] = true
					if !w.attested[r.feature] {
						bisect(w, base, r.feature, budget)
						k.recovered[fired] = true
					}
					// An attested dimension is named but not searchable: the
					// adversary cannot vary it to find where it turns over.
				default:
					// Nothing named. The adversary knows only that the
					// application it just submitted was declined. It can
					// attribute that to the feature it moved — but only when
					// the rule that fired is actually on that feature.
					if r.feature == f {
						k.discovered[fired] = true
						bisect(w, base, r.feature, budget)
						k.recovered[fired] = true
					}
				}
				if k.recovered[fired] {
					// Hold it satisfied so the next rule becomes reachable.
					if !w.attested[r.feature] {
						if r.above {
							base[r.feature] = 0
						} else {
							base[r.feature] = scale
						}
					}
					progressed = true
				}
			}
		}
		if !progressed {
			break
		}
	}
	return k
}

func bisect(w *world, base map[int]int, f int, budget int) {
	lo, hi := 0, scale
	probe := clone(base)
	probe[f] = hi
	up, _ := w.decide(probe)
	for lo < hi && w.queries < budget {
		mid := (lo + hi) / 2
		probe = clone(base)
		probe[f] = mid
		d, _ := w.decide(probe)
		if d == up {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
}

func clone(m map[int]int) map[int]int {
	out := make(map[int]int, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func main() {
	const nRules, trials = 8, 40
	var b strings.Builder
	fmt.Fprintf(&b, "Adversary input control and what a notice gives away\n")
	fmt.Fprintf(&b, "Target: %d knockout rules over %d features. %d trials per cell.\n", nRules, nFeatures, trials)
	fmt.Fprintf(&b, "Attested features are supplied outside the adversary's control and never shown.\n")
	fmt.Fprintf(&b, "recovered = feature and threshold known · discovered = rule known to exist\n\n")
	fmt.Fprintf(&b, "  %9s", "attested")
	for _, p := range []policy{full, reason, outcome} {
		fmt.Fprintf(&b, " %22s", p.String())
	}
	fmt.Fprintf(&b, "\n  %9s", "")
	for range 3 {
		fmt.Fprintf(&b, " %11s%11s", "recovered", "discovered")
	}
	fmt.Fprintln(&b)

	for _, frac := range []float64{0, 0.25, 0.5, 0.75} {
		fmt.Fprintf(&b, "  %8.0f%%", frac*100)
		for _, p := range []policy{full, reason, outcome} {
			var rec, disc float64
			for t := 0; t < trials; t++ {
				rng := rand.New(rand.NewSource(int64(t) + 17))
				w := buildWorld(nRules, frac, rng)
				k := attack(w, p, maxQuery)
				rec += float64(len(k.recovered)) / float64(nRules)
				disc += float64(len(k.discovered)) / float64(nRules)
			}
			fmt.Fprintf(&b, " %10.0f%%%10.0f%%", rec/trials*100, disc/trials*100)
		}
		fmt.Fprintln(&b)
	}

	fmt.Fprint(&b, `
Reading it

  The left column of each policy is the extraction question the first three
  experiments asked, and it answers them the same way when nothing is attested:
  an adversary who controls every input recovers the system whatever the notice
  says, and disclosure is a convenience.

  The columns diverge as inputs move out of reach. A threshold on a quantity
  the requester cannot set is not findable by search at any budget — but a
  notice that prints it hands it over in one query. That gap is the whole of
  what disclosure costs, and it is invisible in any experiment where the
  adversary controls everything.

  The discovered columns matter separately. Knowing that an internal tier or a
  margin floor exists is worth something even without its value: it tells an
  adversary which quantity to go and influence by other means. Outcome-only
  disclosure is the only policy that withholds it.
`)
	os.Stdout.WriteString(b.String())
}

func buildWorld(nRules int, frac float64, rng *rand.Rand) *world {
	attested := map[int]bool{}
	perm := rng.Perm(nFeatures)
	for i := 0; i < int(frac*nFeatures); i++ {
		attested[perm[i]] = true
	}
	rules := make([]rule, 0, nRules)
	rp := rng.Perm(nFeatures)
	for i := 0; i < nRules; i++ {
		rules = append(rules, rule{
			feature: rp[i%nFeatures],
			thresh:  scale/4 + rng.Intn(scale/2),
			above:   rng.Intn(2) == 0,
		})
	}
	return &world{rules: rules, attested: attested, attestRand: rand.New(rand.NewSource(99))}
}
