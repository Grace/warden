# What a customer is told, and what they are not.
#
# These scenarios are the contract as an analyst reads it. They run against the
# real projection, so a change to the ruleset or the contract that alters what a
# customer sees fails here rather than in a support queue.

Feature: shipping eligibility, as customers and staff see it

  Scenario: a package over the mass limit says so, in units and numbers
    Given the ruleset "shipping-eligibility"
    And the outcome "DENY"
    And rule "RL_PKG_MASS_OVER_LIMIT" fires with:
      | PkgMassKg | 34.5 |
      | MaxMassKg | 30   |
    When published to "public"
    Then the outcome is "not_eligible"
    And reason "PACKAGE_TOO_HEAVY" is present
    And the message for "PACKAGE_TOO_HEAVY" is "This package is 34.5 kg; the limit is 30 kg."
    And reason "PACKAGE_TOO_HEAVY" has fact "limit" as "30"

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

  Scenario: staff see the margin rule that customers do not
    Given the ruleset "shipping-eligibility"
    And the outcome "DENY"
    And rule "RL_CARRIER_MARGIN_FLOOR" fires with:
      | MarginBps | 40  |
      | FloorBps  | 150 |
    When published to "internal"
    Then reason "BELOW_MARGIN_FLOOR" is present
    And the message for "BELOW_MARGIN_FLOOR" is "Margin 40 bps is under the 150 bps floor."

  Scenario: the internal zone code stays internal, though the reason is public
    Given the ruleset "shipping-eligibility"
    And the outcome "DENY"
    And rule "RL_DEST_OUTSIDE_ZONE" fires with:
      | SupportedZones | Z1-Z4 |
      | DestZoneCode   | Z9    |
    When published to "public"
    Then reason "DESTINATION_NOT_SERVED" is present
    And reason "DESTINATION_NOT_SERVED" has fact "served_zones" as "Z1-Z4"
    And reason "DESTINATION_NOT_SERVED" has no fact "zone"

  Scenario: a rule nobody mapped stops the whole decision
    Given the ruleset "shipping-eligibility"
    And the outcome "DENY"
    And rule "RL_SOMEONE_ADDED_THIS_LAST_WEEK" fires
    When published to "public"
    Then publishing is refused
    And publishing is refused because "not in the contract"

  Scenario: a message is never published with a hole in it
    Given the ruleset "shipping-eligibility"
    And the outcome "DENY"
    And rule "RL_PKG_MASS_OVER_LIMIT" fires
    When published to "public"
    Then publishing is refused

  Scenario: an outcome meant for partners does not reach the public
    Given the ruleset "shipping-eligibility"
    And the outcome "HOLD"
    And rule "RL_PKG_MASS_OVER_LIMIT" fires
    When published to "public"
    Then publishing is refused because "cannot be shown to public"
