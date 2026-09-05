# verdict

**An anti-corruption layer for rules engines.**

A rules engine answers in its own vocabulary. Ask it why an application was
declined and you get `RL_DEST_OUTSIDE_ZONE`, a working-memory fact called
`DestZoneCode`, and an outcome token of `DENY`.

Hand that to anyone outside the engine — an internal tool, a partner, a
customer-facing screen — and three things happen. They couple to names you can
no longer rename. They see facts that were never meant to leave. And the moment
someone refactors a ruleset, every consumer breaks at once.

`verdict` sits between the engine and everyone else. It translates a decision
into a published contract, scoped to who is asking, and refuses to emit anything
the contract has not explicitly declared.

```
$ verdict project -contract shipping.json -trace decision.json -audience public
{
  "contract_version": "1",
  "outcome": "not_eligible",
  "reasons": [
    {
      "code": "DESTINATION_NOT_SERVED",
      "message": "We do not ship to this destination yet. Served zones: Z1-Z4.",
      "facts": { "served_zones": "Z1-Z4" }
    },
    {
      "code": "PACKAGE_TOO_HEAVY",
      "message": "This package is 34.5 kg; the limit is 30 kg.",
      "facts": { "limit": "30", "mass": "34.5" }
    }
  ]
}
```

The same trace, asked for internally, also carries the margin rule that priced
the shipment, the raw destination zone, and the engine's ruleset version. The
public caller never learns those exist.

## Why a layer and not a view

The usual fix is a response DTO — map the engine's output to a nicer struct on
the way out. That works until the second consumer, and then the mapping lives in
two places and drifts.

The difference here is that the mapping is **data, not code**, and it is
**deny-by-default**. A rule that fires but is not in the contract does not get
dropped quietly; it stops the whole projection. That is deliberate: a decision
published with one of its reasons silently missing is worse than no decision at
all, because it states a conclusion whose causes are not the real ones.

This is Evans's anti-corruption layer, applied to the one system in most
architectures whose vocabulary is *guaranteed* to be unstable — the one business
analysts edit.

## Engine-agnostic

`verdict` never talks to an engine. It consumes a decision trace as JSON:

```json
{
  "ruleset": "shipping-eligibility",
  "version": "2026.09.1",
  "outcome": "DENY",
  "firings": [
    { "rule": "RL_PKG_MASS_OVER_LIMIT",
      "facts": { "PkgMassKg": 34.5, "MaxMassKg": 30 } }
  ]
}
```

Anything that can emit which rules fired and with what facts can feed it —
Drools, a DMN engine, json-rules-engine, OPA, or a hand-rolled decision table.
Adapting an engine means writing that JSON, not linking a library.

## The contract

```json
{
  "contract_version": "1",
  "ruleset": "shipping-eligibility",
  "outcomes": {
    "DENY": { "as": "not_eligible", "audience": "public" },
    "HOLD": { "as": "under_review", "audience": "partner" }
  },
  "rules": {
    "RL_PKG_MASS_OVER_LIMIT": {
      "audience": "public",
      "reason_code": "PACKAGE_TOO_HEAVY",
      "message": "This package is {mass} kg; the limit is {limit} kg.",
      "facts": {
        "PkgMassKg": { "as": "mass",  "audience": "public" },
        "MaxMassKg": { "as": "limit", "audience": "public" }
      }
    }
  }
}
```

Three audiences — `internal`, `partner`, `public` — ordered by how far a term may
travel. A term is visible when its declared audience is at least as wide as the
viewer's, so internal callers see everything and public callers see only what was
deliberately published.

### What it refuses

Everything below is an error at load or at projection, never a silent omission:

| | |
|---|---|
| Unknown field in a contract | a typo'd `audiance` must not default to something wider |
| Unknown audience value | `"audience": "everyone"` |
| A rule fires that the contract doesn't map | the reasons would be incomplete |
| An outcome the contract doesn't map | |
| An outcome too narrow for the viewer | a `partner` outcome asked for by `public` |
| A fact declared wider than its own rule | it would ride out on a reason that can't carry it |
| A `{placeholder}` no fact declares | the message would render with a hole in it |
| A fact whose value is an object or array | nested shape is engine shape |

Contracts are validated at load time, before a single request is served, so a
mistake fails in front of whoever made it rather than three hours later in
production.

## Linting for leaks

Projection can only refuse terms the contract never declared. It cannot see a
term that *was* declared — but declared to a string that still carries the
engine's names inside it. `verdict lint` catches that:

```
$ verdict lint -contract leaky.json
LEAKS  shipping-eligibility — 8 finding(s)

  RL_DEST_OUTSIDE_ZONE         reason_code is the engine rule id
                               consumers would couple to a name you cannot rename later
  RL_DEST_OUTSIDE_ZONE         message contains a dotted path
                               "com.example.rules.zoneTable" looks like a class or package name
  RL_DEST_OUTSIDE_ZONE         fact "DestZoneCode" is published under its engine name
                               give it a contract name so the engine can rename its own
  RL_PKG_MASS_OVER_LIMIT       published with no message
                               a bare code makes the consumer ask a human what it means
  ALLOW                        outcome is published under its engine token
                               map it to a contract term so the engine can change its own
```

It exits non-zero, so it belongs in CI next to your tests. The leak you are
trying to prevent is not usually a missing mapping — it's a lazy one.

## Testing a contract in business language

Projection and lint check the contract against itself. Neither can tell you
whether it says what someone *meant* — and the person who knows that usually
cannot read the ruleset, which is why they are using a contract in the first
place.

So scenarios, in Gherkin, executed against the real projection:

```gherkin
Feature: shipping eligibility, as customers and staff see it

  Scenario: customers are never told about our margins
    Given the ruleset "shipping-eligibility"
    And the outcome "DENY"
    And rule "RL_PKG_MASS_OVER_LIMIT" fires with:
      | PkgMassKg | 34.5 |
      | MaxMassKg | 30   |
    And rule "RL_CARRIER_MARGIN_FLOOR" fires with:
      | MarginBps | 40  |
      | FloorBps  | 150 |
    When published to "public"
    Then reason "BELOW_MARGIN_FLOOR" is absent
    And there is 1 reason
```

```
$ verdict test -contract shipping.json features/*.feature
shipping eligibility, as customers and staff see it
  ok    a package over the mass limit says so, in units and numbers
  ok    customers are never told about our margins
  ok    staff see the margin rule that customers do not
  …
7 scenario(s) passed
```

**A scenario has one foot on each side of the boundary, and that is the point.**
The `Given` steps describe an engine trace in the engine's vocabulary, written
by whoever owns the ruleset. The `Then` steps describe what one audience sees,
in contract vocabulary, read by whoever owns the contract. The scenario is the
boundary, written down — which also means it does not break when someone
refactors the ruleset, since it asserts on published terms.

**No step definitions to write.** Standard Cucumber asks for step definitions in
code, which defeats the purpose here: the person who needs to read these is the
one who cannot read Go. The vocabulary is fixed and wired in — `verdict steps`
prints it — and an unrecognised step is an error rather than a skip, because a
scenario that quietly asserts nothing is worse than none at all.

Failures say what is actually there, not only that expectations were missed:

```
  FAIL  the analyst expected different wording
        shipping.feature:12  And reason "PACKAGE_TOO_HEAVY" has fact "weight" as "34.5"
          reason "PACKAGE_TOO_HEAVY" has no published fact "weight"; it has "limit", "mass"
```

Refusal is assertable too — `Then publishing is refused` — so a contract that
fails closed can prove it does.

## Use

```sh
go install github.com/Grace/verdict@latest

verdict validate -contract shipping.json                     # does it load?
verdict lint     -contract shipping.json                     # any engine vocabulary left?
verdict project  -contract shipping.json -trace d.json \
                 -audience public                            # translate a decision
verdict test     -contract shipping.json features/*.feature  # run scenarios
verdict steps                                                # the step vocabulary
```

Single static binary, stdlib only, no runtime dependencies.

## Status

Early. The projection and contract semantics are settled and tested; what isn't
built yet is the part that would make this a service rather than a tool — an
HTTP surface, contract versioning across more than one `contract_version`
string, and adapters that read a given engine's native trace format instead of
the normalized JSON above.

## Contributing and contact

Issues and pull requests welcome, and [GitHub Discussions](https://github.com/Grace/verdict/discussions)
is the right place for questions and design arguments.

**Using this for something real?** I would like to hear about it — what you
pointed it at, what broke, and what you needed that is not there.

**grace@gracefulco.de**

## License

MIT.
