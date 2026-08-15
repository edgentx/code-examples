Feature: The stream of events is the record

  In order that an audit can be answered from the record itself rather than from
  a state table that was overwritten
  As a records office
  I want every request to be a stream of facts and everything else to be derived
  from it
  So that a request can be rebuilt exactly, a read model can be discarded and
  rebuilt, and the message that told another service what happened is the event
  that happened

  Background:
    Given the "midtown" records office employs "c.hall" as a clerk
    And the "midtown" records office employs "r.okafor" as a records officer

  Scenario: A request is rebuilt from its events alone
    Given request "PRR-2026-0041" has been acknowledged and assigned to "records.officer.7"
    When "r.okafor" releases 18 pages of "PRR-2026-0041"
    And the outbox is drained
    Then the request rebuilt from the stream matches the one that wrote it
    And request "PRR-2026-0041" has 5 events

  Scenario: The list is rebuilt after the read model is discarded
    Given request "PRR-2026-0041" has been acknowledged and assigned to "records.officer.7"
    And "m.alvarez" filed "PRR-2026-0100" through the public portal
    And "r.okafor" sees 2 requests
    When the read model is discarded and rebuilt from the stream
    Then the rebuilt read model matches the one that was discarded

  Scenario: Every fact records the command that caused it
    Given "c.hall" files request "PRR-2026-0041" for "M. Alvarez" describing "bridge inspection reports"
    When "c.hall" acknowledges "PRR-2026-0041"
    Then each event of "PRR-2026-0041" records a traceparent and an idempotency key
    And the two events of "PRR-2026-0041" were caused by different commands
