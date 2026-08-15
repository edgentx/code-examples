Feature: Two officers working the same request

  In order that no decision is silently replaced by one made without knowledge
  of it
  As a records officer
  I want a change decided against a version that has moved on to be refused
  So that the officer is shown what changed and decides again, rather than
  overwriting a colleague and never finding out

  Background:
    Given the "midtown" records office employs "c.hall" as a clerk
    And the "midtown" records office employs "r.okafor" as a records officer
    And the "midtown" records office employs "j.pike" as a records officer
    And request "PRR-2026-0041" has been acknowledged and assigned to "records.officer.7"

  Scenario: The second of two edits decided against the same version is refused
    Given "r.okafor" has read "PRR-2026-0041" at version 3
    And "j.pike" has read "PRR-2026-0041" at version 3
    When "r.okafor" assigns "records.officer.9" to "PRR-2026-0041" at the version they read
    And "j.pike" assigns "records.officer.4" to "PRR-2026-0041" at the version they read
    Then the command is refused because the record changed
    And request "PRR-2026-0041" has 4 events
    And request "PRR-2026-0041" is assigned to "records.officer.9"

  Scenario: A refused edit leaves nothing behind, not even a message
    Given "r.okafor" has read "PRR-2026-0041" at version 3
    And every message so far has been published
    When "c.hall" acknowledges "PRR-2026-0041" at version 2
    Then the command is refused because the record changed
    And 0 messages are waiting to be published

  Scenario: Eight officers deciding at once produce one decision
    When 8 officers assign a reviewer to "PRR-2026-0041" from version 3 at once
    Then 1 of the 8 commands was applied
    And request "PRR-2026-0041" has 4 events

  Scenario: Reading again and deciding again succeeds
    Given "r.okafor" has read "PRR-2026-0041" at version 3
    And "j.pike" has read "PRR-2026-0041" at version 3
    When "r.okafor" assigns "records.officer.9" to "PRR-2026-0041" at the version they read
    And "j.pike" assigns "records.officer.4" to "PRR-2026-0041" at the version they read
    And "j.pike" reads "PRR-2026-0041" again and assigns "records.officer.4" at that version
    Then the command is accepted
    And request "PRR-2026-0041" is assigned to "records.officer.4"
    And request "PRR-2026-0041" has 5 events
