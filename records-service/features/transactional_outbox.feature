Feature: A fact that was recorded is always announced, and announced once

  In order that a records office and the service that delivers its packages never
  disagree about what happened
  As a records office
  I want the message announcing a change written in the same transaction as the
  change
  So that a machine losing power between the two cannot leave a change nobody
  hears about, or an announcement of a change that was rolled back

  Background:
    Given the "midtown" records office employs "c.hall" as a clerk
    And the "midtown" records office employs "r.okafor" as a records officer

  Scenario: Recording a fact queues its message in the same transaction
    When "c.hall" files request "PRR-2026-0041" for "M. Alvarez" describing "bridge inspection reports"
    Then 1 message is waiting to be published
    And the waiting message is a CloudEvent of type "records_request.submitted"
    And the waiting message carries a traceparent

  Scenario: A refused change queues no message
    Given request "PRR-2026-0041" has been acknowledged and assigned to "records.officer.7"
    And every message so far has been published
    When "c.hall" acknowledges "PRR-2026-0041" at version 1
    Then the command is refused because the record changed
    And 0 messages are waiting to be published

  Scenario: A process that dies before publishing loses nothing
    Given request "PRR-2026-0041" has been acknowledged and assigned to "records.officer.7"
    And "r.okafor" releases 18 pages of "PRR-2026-0041"
    When the process dies before the relay runs
    And the relay is restarted and drains
    Then request "PRR-2026-0041" is "fulfilled"
    And exactly 1 package has been assembled

  Scenario: A process that dies between publishing and recording the dispatch has one effect
    Given request "PRR-2026-0041" has been acknowledged and assigned to "records.officer.7"
    And "r.okafor" releases 18 pages of "PRR-2026-0041"
    When the relay publishes and then dies before recording the dispatch
    Then the release message is still waiting to be published
    When the relay is restarted and drains
    Then the release message was delivered twice
    And exactly 1 package has been assembled
    And request "PRR-2026-0041" is "fulfilled"
    And request "PRR-2026-0041" has 5 events
