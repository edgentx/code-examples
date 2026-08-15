Feature: The lifecycle a public records request may follow

  In order to answer requests in an order an agency can defend on appeal
  As a records officer
  I want each step to be possible only when the step before it has happened
  So that no request is worked before it is acknowledged, answered without an
  accountable officer, or changed after it is closed

  Background:
    Given the "midtown" records office employs "c.hall" as a clerk
    And the "midtown" records office employs "r.okafor" as a records officer

  Scenario: A request is acknowledged, assigned, released, and delivered
    Given "c.hall" files request "PRR-2026-0041" for "M. Alvarez" describing "bridge inspection reports"
    Then request "PRR-2026-0041" is "open"

    When "c.hall" acknowledges "PRR-2026-0041"
    Then request "PRR-2026-0041" is "acknowledged"

    When "r.okafor" assigns "records.officer.7" to "PRR-2026-0041"
    And "r.okafor" releases 18 pages of "PRR-2026-0041"
    Then request "PRR-2026-0041" is "release_pending"

    When the outbox is drained
    Then request "PRR-2026-0041" is "fulfilled"
    And request "PRR-2026-0041" shows 18 released pages

  Scenario: A request is denied with a citation
    Given request "PRR-2026-0041" has been acknowledged and assigned to "records.officer.7"
    When "r.okafor" denies "PRR-2026-0041" citing "personnel file exemption"
    Then request "PRR-2026-0041" is "denied"

  Scenario: Work is refused before the statutory receipt notice has gone out
    Given "c.hall" files request "PRR-2026-0041" for "M. Alvarez" describing "bridge inspection reports"
    When "r.okafor" assigns "records.officer.7" to "PRR-2026-0041"
    Then the command is refused because "records request has not been acknowledged"
    And request "PRR-2026-0041" has 1 event

  Scenario: A second receipt notice is refused
    Given request "PRR-2026-0041" has been acknowledged and assigned to "records.officer.7"
    When "c.hall" acknowledges "PRR-2026-0041"
    Then the command is refused because "records request has already been acknowledged"

  Scenario: Records are not released without an accountable officer
    Given "c.hall" files request "PRR-2026-0041" for "M. Alvarez" describing "bridge inspection reports"
    And "c.hall" acknowledges "PRR-2026-0041"
    When "r.okafor" releases 18 pages of "PRR-2026-0041"
    Then the command is refused because "records request has no assigned reviewer"

  Scenario: A denial without a citation is refused
    Given request "PRR-2026-0041" has been acknowledged and assigned to "records.officer.7"
    When "r.okafor" denies "PRR-2026-0041" citing ""
    Then the command is refused because "required field is missing"

  Scenario: Nothing further is accepted once a request is closed
    Given request "PRR-2026-0041" has been acknowledged and assigned to "records.officer.7"
    And "r.okafor" denies "PRR-2026-0041" citing "personnel file exemption"
    When "r.okafor" releases 4 pages of "PRR-2026-0041"
    Then the command is refused because "records request is closed"

  Scenario: An officer command is refused while a release is out for delivery
    Given request "PRR-2026-0041" has a release awaiting delivery
    When "r.okafor" denies "PRR-2026-0041" citing "personnel file exemption"
    Then the command is refused because "records request has a release awaiting delivery"
