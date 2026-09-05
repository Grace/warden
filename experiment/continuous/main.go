// Command continuous tests whether the earlier findings survive a model class
// where they should not.
//
// Two experiments so far said explanation disclosure is worth a small constant
// factor to an extraction adversary. Both used decision systems with axis-
// aligned thresholds an adversary could probe one feature at a time — rule
// lists and linear scorecards, which is what underwriting looks like but not
// what the extraction literature studies.
//
// That literature reports high-fidelity surrogates from roughly 500 queries
// against continuous models, and attributes it to counterfactual explanations:
// a counterfactual is a point *on the decision boundary*, handed over for free.
// Random sampling has to find the boundary; a counterfactual is one.
//
// If the constant-factor result holds here too, it generalises. If the gap is
// large, the honest conclusion is that disclosure risk is a property of the
// model class and the explanation type, not of explanation as such — which is
// a more useful thing to be able to tell a client than either extreme.
package main

import (
	"fmt"
	"math"
	"math/rand"
	"os"
	"strings"
)

const (
	dim    = 8
	hidden = 12
)

// net is a small tanh network — a decision boundary with curvature, which is
// the property axis-aligned rules lacked.
type net struct {
	w1 [hidden][dim]float64
	b1 [hidden]float64
	w2 [hidden]float64
	b2 float64
}

func newNet(rng *rand.Rand, spread float64) *net {
	var n net
	for h := 0; h < hidden; h++ {
		for d := 0; d < dim; d++ {
			n.w1[h][d] = rng.NormFloat64() * spread
		}
		n.b1[h] = rng.NormFloat64() * spread
		n.w2[h] = rng.NormFloat64() * spread
	}
	return &n
}

func (n *net) score(x []float64) float64 {
	s := n.b2
	for h := 0; h < hidden; h++ {
		a := n.b1[h]
		for d := 0; d < dim; d++ {
			a += n.w1[h][d] * x[d]
		}
		s += n.w2[h] * math.Tanh(a)
	}
	return s
}

func (n *net) approve(x []float64) bool { return n.score(x) >= 0 }

// train fits the surrogate by SGD on collected (input, label) pairs. Points
// known to sit on the boundary are supplied with a soft target, which is what
// makes a counterfactual worth more than a labelled sample.
func (n *net) train(xs [][]float64, ys []float64, epochs int, lr float64, rng *rand.Rand) {
	idx := make([]int, len(xs))
	for i := range idx {
		idx[i] = i
	}
	for e := 0; e < epochs; e++ {
		rng.Shuffle(len(idx), func(i, j int) { idx[i], idx[j] = idx[j], idx[i] })
		for _, i := range idx {
			x, y := xs[i], ys[i]
			// forward
			var a, z [hidden]float64
			s := n.b2
			for h := 0; h < hidden; h++ {
				v := n.b1[h]
				for d := 0; d < dim; d++ {
					v += n.w1[h][d] * x[d]
				}
				a[h] = v
				z[h] = math.Tanh(v)
				s += n.w2[h] * z[h]
			}
			p := 1 / (1 + math.Exp(-s))
			g := p - y // logistic loss gradient
			n.b2 -= lr * g
			for h := 0; h < hidden; h++ {
				gh := g * n.w2[h] * (1 - z[h]*z[h])
				n.w2[h] -= lr * g * z[h]
				n.b1[h] -= lr * gh
				for d := 0; d < dim; d++ {
					n.w1[h][d] -= lr * gh * x[d]
				}
			}
		}
	}
}

func sample(rng *rand.Rand) []float64 {
	x := make([]float64, dim)
	for d := range x {
		x[d] = rng.NormFloat64()
	}
	return x
}

// counterfactual returns the nearest approved point along the segment toward a
// known approved reference — the standard "what would have to change" answer,
// and a point that sits on the decision boundary by construction.
func counterfactual(t *net, denied, ref []float64) []float64 {
	lo, hi := 0.0, 1.0
	for i := 0; i < 24; i++ {
		mid := (lo + hi) / 2
		p := make([]float64, dim)
		for d := range p {
			p[d] = denied[d] + mid*(ref[d]-denied[d])
		}
		if t.approve(p) {
			hi = mid
		} else {
			lo = mid
		}
	}
	out := make([]float64, dim)
	for d := range out {
		out[d] = denied[d] + hi*(ref[d]-denied[d])
	}
	return out
}

type mode int

const (
	withCF mode = iota
	outcomeOnly
)

func (m mode) String() string {
	return [...]string{"counterfactual disclosed", "outcome only"}[m]
}

// extract collects `budget` queries under a policy and returns surrogate
// agreement with the target.
func extract(t *net, m mode, budget int, rng *rand.Rand) float64 {
	var xs [][]float64
	var ys []float64

	// A reference approved point, found by sampling. Charged to the budget.
	var ref []float64
	used := 0
	for used < budget && ref == nil {
		x := sample(rng)
		used++
		if t.approve(x) {
			ref = x
		}
	}
	if ref == nil {
		return 0.5
	}

	for used < budget {
		x := sample(rng)
		used++
		ok := t.approve(x)
		xs = append(xs, x)
		if ok {
			ys = append(ys, 1)
			continue
		}
		ys = append(ys, 0)
		if m == withCF {
			// One notice, one boundary point. The bisection is the *provider's*
			// work in generating the explanation, not the adversary's queries —
			// which is exactly why a counterfactual is cheap to receive and
			// expensive to have given away.
			cf := counterfactual(t, x, ref)
			xs = append(xs, cf)
			ys = append(ys, 0.5)
		}
	}

	sur := newNet(rand.New(rand.NewSource(99)), 0.3)
	sur.train(xs, ys, 400, 0.05, rand.New(rand.NewSource(7)))
	return agreement(t, sur, rand.New(rand.NewSource(11)))
}

func agreement(a, b *net, rng *rand.Rand) float64 {
	const n = 20000
	hit := 0
	for i := 0; i < n; i++ {
		x := sample(rng)
		if a.approve(x) == b.approve(x) {
			hit++
		}
	}
	return float64(hit) / n
}

func main() {
	var b strings.Builder
	fmt.Fprintf(&b, "Continuous model: does a counterfactual change the picture?\n")
	fmt.Fprintf(&b, "Target: %d-dim tanh network, %d hidden units. Surrogate: same architecture.\n", dim, hidden)
	fmt.Fprintf(&b, "Extraction = surrogate agreement with the target on held-out inputs.\n")
	fmt.Fprintf(&b, "Median of 7 random targets per cell.\n\n")
	fmt.Fprintf(&b, "  %8s %14s %14s %10s\n", "queries", "outcome only", "counterfactual", "gain")

	for _, budget := range []int{50, 100, 250, 500, 1000, 2000} {
		var oc, cf []float64
		for trial := 0; trial < 7; trial++ {
			t := newNet(rand.New(rand.NewSource(int64(trial)+41)), 0.9)
			oc = append(oc, extract(t, outcomeOnly, budget, rand.New(rand.NewSource(5))))
			cf = append(cf, extract(t, withCF, budget, rand.New(rand.NewSource(5))))
		}
		o, c := median(oc), median(cf)
		fmt.Fprintf(&b, "  %8d %13.0f%% %13.0f%% %9.1fpp\n", budget, o*100, c*100, (c-o)*100)
	}

	fmt.Fprint(&b, `
Reading it

  A counterfactual is a boundary point handed over on request. Random sampling
  has to find the boundary by bracketing it between labelled points, which in
  eight dimensions is most of the work. If disclosure matters anywhere, it
  matters here — and if the gain is small here too, then the constant-factor
  result from the rule-list experiments is not an artifact of axis-aligned
  thresholds.
`)
	os.Stdout.WriteString(b.String())
}

func median(xs []float64) float64 {
	c := append([]float64(nil), xs...)
	for i := 1; i < len(c); i++ {
		for j := i; j > 0 && c[j] < c[j-1]; j-- {
			c[j], c[j-1] = c[j-1], c[j]
		}
	}
	return c[len(c)/2]
}
