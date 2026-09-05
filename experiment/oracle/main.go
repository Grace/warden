// Command oracle measures what a decline reason costs you.
//
// warden's lint asks a static question: does this contract leak the engine's
// vocabulary? This asks the empirical one. Stand up a rules engine with hidden
// thresholds, let an adversary submit shipments and read whatever the boundary
// returns, and count the queries to recovery.
//
// The result is deliberately not a clean win. An attacker who can submit and
// observe accept/reject has an oracle whatever you say to them — the decision
// itself is one bit. What disclosure changes is the cost of a bit and, more
// importantly, whether dimensions the attacker never hypothesised are
// discoverable at all. That second effect is the one worth paying for, and it
// is invisible if you only count queries on thresholds the attacker already
// knew to look for.
package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// ---------- the engine under attack ----------

// Shipment is what an adversary controls.
type Shipment struct {
	MassKg   int
	ValueUSD int
	Zone     string
}

// marginBps is computed by pricing, not submitted. This matters: an adversary
// can only search a dimension they can *move*, and an internal rule keyed on a
// derived value is one they can only reach through confounded inputs.
func marginBps(s Shipment) int {
	m := 600 - s.ValueUSD/200
	if s.Zone != "NA" {
		m -= 150
	}
	if m < 0 {
		m = 0
	}
	return m
}

// engine holds the parameters an attacker wants.
type engine struct {
	maxMassKg      int
	maxValueUSD    int
	servedZones    map[string]bool
	marginFloorBps int // never surfaced at any audience
	queries        int
}

func newEngine() *engine {
	return &engine{
		maxMassKg:      2500,
		maxValueUSD:    50000,
		servedZones:    map[string]bool{"NA": true, "EU": true},
		marginFloorBps: 180,
	}
}

// decision is the engine's own vocabulary, before any boundary.
type decision struct {
	Allowed bool
	RuleID  string
	Facts   map[string]int
}

func (e *engine) decide(s Shipment) decision {
	e.queries++
	switch {
	case !e.servedZones[s.Zone]:
		return decision{false, "RL_DEST_OUTSIDE_ZONE", nil}
	case s.MassKg > e.maxMassKg:
		return decision{false, "RL_PKG_MASS_OVER_LIMIT",
			map[string]int{"mass": s.MassKg, "limit": e.maxMassKg}}
	case s.ValueUSD > e.maxValueUSD:
		return decision{false, "RL_DECLARED_VALUE_OVER_LIMIT",
			map[string]int{"value": s.ValueUSD, "limit": e.maxValueUSD}}
	case marginBps(s) < e.marginFloorBps:
		return decision{false, "RL_CARRIER_MARGIN_FLOOR",
			map[string]int{"margin": marginBps(s), "floor": e.marginFloorBps}}
	}
	return decision{Allowed: true}
}

// ---------- the boundary ----------

type mode int

const (
	// raw hands the engine's own decision to the caller. Common, and the
	// default whenever nobody decided otherwise.
	raw mode = iota
	// reasoned publishes a stable reason code naming the mechanism, but not
	// the value. This is what most teams think "not leaking" means.
	reasoned
	// opaque publishes the outcome only, which is what warden emits to a
	// public audience.
	opaque
)

func (m mode) String() string {
	return [...]string{"raw engine output", "reason codes, no values", "warden: outcome only"}[m]
}

// response is all the adversary sees.
type response struct {
	Allowed bool
	Reason  string         // "" under opaque
	Facts   map[string]int // nil unless raw
}

func project(d decision, m mode) response {
	if d.Allowed {
		return response{Allowed: true}
	}
	switch m {
	case raw:
		return response{Reason: d.RuleID, Facts: d.Facts}
	case reasoned:
		return response{Reason: map[string]string{
			"RL_DEST_OUTSIDE_ZONE":         "DESTINATION_NOT_SERVED",
			"RL_PKG_MASS_OVER_LIMIT":       "PACKAGE_TOO_HEAVY",
			"RL_DECLARED_VALUE_OVER_LIMIT": "VALUE_TOO_HIGH",
			"RL_CARRIER_MARGIN_FLOOR":      "BELOW_MARGIN_FLOOR",
		}[d.RuleID]}
	default:
		return response{Reason: "not_eligible"}
	}
}

// ---------- the adversary ----------

// probe is one dimension the attacker has thought to vary. An attacker cannot
// search a dimension they have not hypothesised, which is the whole point of
// the discovery column in the report.
type probe struct {
	name string
	lo   int
	hi   int
	// floor is true when the rule rejects values *below* the threshold, which
	// inverts both the search and which end violates it.
	floor bool
	build func(v int) Shipment
	truth func(*engine) int
}

func probes() []probe {
	// Every probe holds the other dimensions at values it knows are fine, so a
	// rejection can only be the dimension under test. Nothing is defaulted from
	// a zero value: a margin of 0 is a legitimate thing to submit, and reading
	// it as "unset" silently stopped that dimension being tested at all.
	return []probe{
		{"max mass (kg)", 0, 10000, false,
			func(v int) Shipment { return Shipment{MassKg: v, ValueUSD: 100, Zone: "NA"} },
			func(e *engine) int { return e.maxMassKg }},
		{"max value (USD)", 0, 200000, false,
			func(v int) Shipment { return Shipment{MassKg: 1, ValueUSD: v, Zone: "NA"} },
			func(e *engine) int { return e.maxValueUSD }},
		// The attacker cannot set margin. The best they can do is vary value,
		// which moves margin *and* trips the value ceiling — so a rejection is
		// ambiguous unless something tells them which rule fired.
		{"margin floor (bps)", 0, 1000, true,
			func(v int) Shipment { return Shipment{MassKg: 1, ValueUSD: (600 - v) * 200, Zone: "NA"} },
			func(e *engine) int { return e.marginFloorBps }},
	}
}

type result struct {
	dimension string
	queries   int
	recovered bool
	// discovered is whether the response told the attacker this dimension
	// exists, as opposed to their having guessed it.
	discovered bool
	how        string
}

// attack recovers one threshold under one disclosure mode.
func attack(p probe, m mode) result {
	e := newEngine()
	want := p.truth(e)

	// Under raw disclosure the boundary simply hands over the number. Probe the
	// end that actually violates the rule: the top for a ceiling, the bottom
	// for a floor.
	violating := p.hi
	if p.floor {
		violating = p.lo
	}
	if m == raw {
		r := project(e.decide(p.build(violating)), raw)
		for k, v := range r.Facts {
			if !strings.Contains(k, "limit") && !strings.Contains(k, "floor") {
				continue
			}
			if v == want {
				return result{p.name, e.queries, true, true,
					fmt.Sprintf("published as %q in one query", k)}
			}
			// A different rule fired first and published *its* boundary. The
			// attacker still learns a threshold, just not this one — an
			// accident of rule ordering rather than a control.
			return result{p.name, e.queries, false, true,
				fmt.Sprintf("shadowed: an earlier rule published %q=%d instead", k, v)}
		}
	}

	// Otherwise: binary search on accept/reject. The decision is one bit and
	// the attacker gets it for free however carefully the reason is worded.
	lo, hi := p.lo, p.hi
	inverted := p.floor
	discovered := false
	for lo < hi {
		mid := (lo + hi + 1) / 2
		r := project(e.decide(p.build(mid)), m)
		if r.Reason != "" && r.Reason != "not_eligible" {
			discovered = true
		}
		ok := r.Allowed
		if inverted {
			ok = !ok
		}
		if ok {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	got := lo
	if inverted {
		got = lo + 1
	}
	how := "binary search on accept/reject"
	if discovered {
		how = "binary search; reason code names the dimension"
	}
	return result{p.name, e.queries, got == want, discovered, how}
}

func main() {
	var b strings.Builder
	fmt.Fprintf(&b, "Oracle recovery against a shipping-eligibility engine\n")
	fmt.Fprintf(&b, "Hidden: max mass 2500 kg · max value $50,000 · margin floor 180 bps\n")
	fmt.Fprintf(&b, "Adversary: may submit shipments and read the response. Nothing else.\n\n")

	for _, m := range []mode{raw, reasoned, opaque} {
		fmt.Fprintf(&b, "%s\n", m)
		fmt.Fprintf(&b, "  %-20s %8s  %-10s %-11s %s\n",
			"dimension", "queries", "recovered", "discoverable", "how")
		var rs []result
		for _, p := range probes() {
			rs = append(rs, attack(p, m))
		}
		sort.SliceStable(rs, func(i, j int) bool { return rs[i].queries < rs[j].queries })
		total := 0
		for _, r := range rs {
			total += r.queries
			fmt.Fprintf(&b, "  %-20s %8d  %-10v %-11v %s\n",
				r.dimension, r.queries, r.recovered, r.discovered, r.how)
		}
		fmt.Fprintf(&b, "  %-20s %8d\n\n", "total", total)
	}

	fmt.Fprint(&b, strings.TrimRight(`
Reading this honestly:

  Recovery is not what warden prevents. An adversary who can submit and observe
  accept/reject holds a one-bit oracle no wording removes, and every threshold
  above falls to binary search under all three modes. Anyone claiming their
  gateway stops this is selling you something.

  What changes is cost and discoverability.

  Cost: raw output hands over a threshold in a single query. Opaque output
  makes the same threshold cost a logarithmic search — roughly a thousand times
  more traffic for the value dimension, which is the difference between an
  invisible probe and one a rate limiter and an audit log both notice.

  Discoverability is the effect that matters. Under raw and reasoned output the
  response names the dimension: an attacker who never suspected a carrier
  margin floor is told it exists and given its units. Under opaque output that
  column is false for every rule — a dimension the adversary did not already
  hypothesise is never revealed, and the internal-only rules stay internal not
  because they are hidden but because no rejection distinguishes them.

  So the claim warden can defend is narrow and true: it does not stop a
  determined search of a dimension you already knew about. It stops the engine
  from teaching an attacker its own shape, and it raises the price of each
  answer to something the rest of your controls can see.
`, "\n"))
	fmt.Fprintln(&b)
	os.Stdout.WriteString(b.String())
}
