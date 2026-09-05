package main

import (
	"fmt"
	"math/rand"
	"testing"
)

// A reviewer's objection to experiment 5, tested rather than argued.
//
// The outcome-only adversary in attack() charges every decline to every
// attested feature, and the comment there says it "has to be". It does not.
// A one-sided threshold rule passes an interval: either [0,thresh] or
// [thresh,scale]. So if the adversary has seen feature f take both loApprove
// and hiApprove on approved applications, every value between them also
// passed f — whatever direction f's rule runs, and without being told f's
// rule exists.
//
// That gives a decline-attribution rule needing no reason code: certify each
// attested value against its own observed approval interval, and if exactly
// one attested feature fails to certify, the decline belongs to it. No oracle
// knowledge, no extra queries, derived entirely from the observation log the
// outcome-only adversary already has.
//
// If this recovers what the reason-code adversary recovers, then attribution
// is not the unlock and the paper's sharpest claim is a property of a
// hand-written adversary rather than of disclosure.

// certified reports whether v is known to pass f from approvals alone.
func certified(b *bracket, v int) bool {
	return b != nil && b.seenA && v >= b.loApprove && v <= b.hiApprove
}

// attackSelfCert is attack() with outcome-only disclosure and observable
// attested values, differing only in how a decline is attributed.
func attackSelfCert(w *world, budget int) float64 {
	recovered := map[int]bool{}
	base := map[int]int{}

	// Phase 1 is byte-for-byte the outcome-only path from attack().
	for f := 0; f < nFeatures && w.queries < budget/2; f++ {
		if w.attested[f] {
			continue
		}
		for _, v := range []int{0, scale} {
			probe := clone(base)
			probe[f] = v
			declined, fired, _ := w.submit(probe, true)
			if !declined || recovered[fired] {
				continue
			}
			r := w.rules[fired]
			if r.feature == f {
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

	brackets := map[int]*bracket{}
	for w.queries < budget {
		declined, _, seen := w.submit(base, true)
		for f := range seen {
			if brackets[f] == nil {
				brackets[f] = &bracket{}
			}
		}
		if !declined {
			// An approval clears every attested feature at once.
			for f, v := range seen {
				brackets[f].add(v, false)
			}
			continue
		}
		// A decline clears none of them, but the approval intervals
		// already exonerate most. Charge it only if exactly one attested
		// feature is left unexonerated.
		suspect, n := -1, 0
		for f, v := range seen {
			if certified(brackets[f], v) {
				continue
			}
			suspect, n = f, n+1
		}
		if n == 1 {
			brackets[suspect].add(seen[suspect], true)
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

func TestSelfCertifyingAdversary(t *testing.T) {
	const nRules, trials, frac = 8, 40, 0.5

	fmt.Printf("\nObserved attestation, %d rules over %d features, %.0f%% attested,\n",
		nRules, nFeatures, frac*100)
	fmt.Printf("%d trials per cell. Figures are rules recovered.\n\n", trials)
	fmt.Printf("  %8s %12s %12s %12s %10s\n",
		"budget", "outcome", "outcome+cert", "reason", "cert-reason")

	for _, budget := range []int{200, 1000, 5000, 20000, 100000} {
		var naive, cert, reas float64
		for tr := 0; tr < trials; tr++ {
			seed := int64(tr) + 17
			naive += attack(build(nRules, frac, rand.New(rand.NewSource(seed))), outcome, true, budget)
			reas += attack(build(nRules, frac, rand.New(rand.NewSource(seed))), reason, true, budget)
			cert += attackSelfCert(build(nRules, frac, rand.New(rand.NewSource(seed))), budget)
		}
		n, c, r := naive/trials*100, cert/trials*100, reas/trials*100
		fmt.Printf("  %8d %11.1f%% %11.1f%% %11.1f%% %9.1fpp\n", budget, n, c, r, c-r)
	}
	fmt.Println()
}
