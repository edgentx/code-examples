Feature: What the console is told over HTTP

  In order that an operator is never shown a screen the record does not support
  As a records officer at a console
  I want the API to tell the console the version it read, what the caller may do,
  and plainly when a change was refused because the record moved
  So that a stale edit becomes a visible "the record changed, review and try
  again" rather than an overwrite nobody notices

  # These are the exact exchanges the console makes. The browser half of them --
  # the skeleton, the slide-out panel, the focus move to the conflict notice --
  # is asserted in web/src/__tests__ and web/tests/e2e; this feature is the
  # contract those tests are written against.

  Background:
    Given the "midtown" records office employs "c.hall" as a clerk
    And the "midtown" records office employs "r.okafor" as a records officer
    And request "PRR-2026-0041" has been acknowledged and assigned to "records.officer.7"

  Scenario: Reading a request returns its version and what the caller may do
    When the console reads "PRR-2026-0041" as "r.okafor"
    Then the console is told 200
    And the entity tag is the version 3
    And the console is offered "read, acknowledge, assign_reviewer, release, deny"

  Scenario: A clerk is offered fewer controls than a records officer
    When the console reads "PRR-2026-0041" as "c.hall"
    Then the console is told 200
    And the console is offered "read, acknowledge"

  Scenario: A caller with no relationship to the office is refused
    When the console reads "PRR-2026-0041" as "t.nowak"
    Then the console is told 403 with code "not_authorized"

  Scenario: An unidentified caller is refused before anything is read
    When the console reads "PRR-2026-0041" as nobody
    Then the console is told 401 with code "unidentified_caller"

  Scenario: An edit at the version the console read is applied
    When the console reads "PRR-2026-0041" as "r.okafor"
    And the console assigns "records.officer.9" at the version it read
    Then the console is told 200
    And the entity tag is the version 4

  Scenario: An edit at a version that has moved on is refused and says so
    Given the console has read "PRR-2026-0041" as "r.okafor"
    And "r.okafor" assigns "records.officer.4" to "PRR-2026-0041" meanwhile
    When the console assigns "records.officer.9" at the version it read
    Then the console is told 409 with code "version_conflict"
    And the conflict names the current version 4
    And request "PRR-2026-0041" is assigned to "records.officer.4"
    And request "PRR-2026-0041" has 4 events

  Scenario: Reading again and retrying the same edit succeeds
    Given the console has read "PRR-2026-0041" as "r.okafor"
    And "r.okafor" assigns "records.officer.4" to "PRR-2026-0041" meanwhile
    And the console was told 409 for its edit
    When the console reads "PRR-2026-0041" as "r.okafor"
    And the console assigns "records.officer.9" at the version it read
    Then the console is told 200
    And request "PRR-2026-0041" is assigned to "records.officer.9"

  Scenario: A command with no idempotency key is refused
    Given the console has read "PRR-2026-0041" as "r.okafor"
    When the console assigns "records.officer.9" with no idempotency key
    Then the console is told 400 with code "idempotency_key_required"

  Scenario: A command the domain refuses is not a conflict
    When the console reads "PRR-2026-0041" as "r.okafor"
    And the console assigns "records.officer.7" at the version it read
    Then the console is told 422 with code "rule_violated"

  Scenario: A command the authorization model refuses is not a rule violation
    When the console reads "PRR-2026-0041" as "c.hall"
    And the console releases 18 pages at the version it read
    Then the console is told 403 with code "not_authorized"
