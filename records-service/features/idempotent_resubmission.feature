Feature: A command that arrives twice takes effect once

  In order that a lost response, an impatient operator, or a retried delivery
  never produces a second entry on a record
  As a records office
  I want every command to carry a key that identifies the intent behind it
  So that resending the same command returns what the first one produced instead
  of applying it again or failing on a rule it already satisfied

  Background:
    Given the "midtown" records office employs "c.hall" as a clerk
    And the "midtown" records office employs "r.okafor" as a records officer

  Scenario: A filing sent twice creates one request
    When "c.hall" files request "PRR-2026-0041" for "M. Alvarez" describing "bridge inspection reports"
    And the same filing is sent again under the same idempotency key
    Then the office holds 1 request
    And request "PRR-2026-0041" has 1 event
    And 1 message is waiting to be published

  Scenario: A receipt notice sent twice is one notice, not an error
    Given "c.hall" files request "PRR-2026-0041" for "M. Alvarez" describing "bridge inspection reports"
    When "c.hall" acknowledges "PRR-2026-0041"
    And the same command is sent again under the same idempotency key
    Then the command is accepted
    And request "PRR-2026-0041" is "acknowledged"
    And request "PRR-2026-0041" has 2 events

  Scenario: The same command under a different key is a second command
    Given "c.hall" files request "PRR-2026-0041" for "M. Alvarez" describing "bridge inspection reports"
    When "c.hall" acknowledges "PRR-2026-0041"
    And the same command is sent again under a new idempotency key
    Then the command is refused because "records request has already been acknowledged"
    And request "PRR-2026-0041" has 2 events

  Scenario: Eight simultaneous copies of one filing create one request
    When 8 copies of the same filing are sent at once
    Then the office holds 1 request
    And 1 of the 8 copies was applied
    And 1 message is waiting to be published

  Scenario: A command without a key is refused rather than applied unsafely
    Given "c.hall" files request "PRR-2026-0041" for "M. Alvarez" describing "bridge inspection reports"
    When "c.hall" acknowledges "PRR-2026-0041" with no idempotency key
    Then the command is refused because "write has no idempotency key"
    And request "PRR-2026-0041" has 1 event
