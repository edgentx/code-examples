Feature: A release that spans two services

  In order to hold two systems to one outcome without a coordinator that can
  itself fail
  As a records office
  I want the delivering service to react to the release and report what happened
  So that a request is either closed by a delivery that occurred, or put back in
  front of the officer by a release that was withdrawn

  # Coordination here is choreography: each service reacts to facts and emits its
  # own. There is no coordinating process and no engine. The only way to undo a
  # release is a compensating fact that the records service applies to itself.

  Background:
    Given the "midtown" records office employs "c.hall" as a clerk
    And the "midtown" records office employs "r.okafor" as a records officer
    And request "PRR-2026-0041" has been acknowledged and assigned to "records.officer.7"

  Scenario: The package is delivered and the request is closed
    When "r.okafor" releases 18 pages of "PRR-2026-0041"
    Then request "PRR-2026-0041" is "release_pending"

    When the outbox is drained
    Then exactly 1 package has been assembled
    And request "PRR-2026-0041" is "fulfilled"
    And request "PRR-2026-0041" shows 18 released pages

  Scenario: The package cannot be delivered, so the release is withdrawn
    Given no package can be assembled because "two pages are still under legal hold"
    When "r.okafor" releases 18 pages of "PRR-2026-0041"
    And the outbox is drained
    Then request "PRR-2026-0041" is "release_failed"
    And request "PRR-2026-0041" shows 0 released pages
    And request "PRR-2026-0041" notes "two pages are still under legal hold"

  Scenario: The officer works the request again after a compensation
    Given no package can be assembled because "two pages are still under legal hold"
    And "r.okafor" releases 18 pages of "PRR-2026-0041"
    And the outbox is drained
    When packages can be assembled again
    And "r.okafor" releases 16 pages of "PRR-2026-0041"
    And the outbox is drained
    Then request "PRR-2026-0041" is "fulfilled"
    And request "PRR-2026-0041" shows 16 released pages
    And request "PRR-2026-0041" notes ""

  Scenario: A denial after a compensation is accepted
    Given no package can be assembled because "two pages are still under legal hold"
    And "r.okafor" releases 18 pages of "PRR-2026-0041"
    And the outbox is drained
    When "r.okafor" denies "PRR-2026-0041" citing "personnel file exemption"
    Then request "PRR-2026-0041" is "denied"

  Scenario: The work the delivering service does belongs to the trace that started it
    When "r.okafor" releases 18 pages of "PRR-2026-0041" in a new trace
    And the outbox is drained
    Then the delivery outcome belongs to the trace the release started
    And the delivery outcome is a different span from the release

  Scenario: A temporary failure is retried rather than compensated
    Given the delivering service is temporarily unreachable
    When "r.okafor" releases 18 pages of "PRR-2026-0041"
    And the outbox is drained and fails
    Then request "PRR-2026-0041" is "release_pending"
    And the release message is still waiting to be published

    When the delivering service is reachable again
    And the relay is restarted and drains
    Then request "PRR-2026-0041" is "fulfilled"
