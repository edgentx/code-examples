Feature: Who may do what to a records request

  In order to show an auditor that every action was taken by somebody entitled
  to take it
  As a records office
  I want each action decided by the relationships in the authorization model
  So that access follows from who somebody is to the office and to the request,
  not from a conditional somewhere in the application

  Background:
    Given the "midtown" records office employs "c.hall" as a clerk
    And the "midtown" records office employs "r.okafor" as a records officer
    And the "parkside" records office employs "d.mensah" as a records officer
    And request "PRR-2026-0041" has been acknowledged and assigned to "records.officer.7"

  Scenario Outline: The action is permitted and it is taken
    When "<who>" attempts to "<action>" "PRR-2026-0041"
    Then the command is accepted

    Examples: clerical work is clerical
      | who    | action      |
      | c.hall | read        |
      | r.okafor | read      |

    Examples: deciding the answer is the records officer's
      | who      | action  |
      | r.okafor | release |

  Scenario Outline: The action is not permitted and nothing happens
    When "<who>" attempts to "<action>" "PRR-2026-0041"
    Then the command is refused because the caller is not authorized
    And request "PRR-2026-0041" has 3 events

    Examples: a clerk may take in work but may not decide it
      | who    | action  |
      | c.hall | release |
      | c.hall | deny    |
      | c.hall | assign  |

    Examples: an officer of another office reaches nothing here
      | who      | action  |
      | d.mensah | read    |
      | d.mensah | release |

    Examples: somebody with no relationship to the office reaches nothing
      | who     | action  |
      | t.nowak | read    |
      | t.nowak | release |

  Scenario: A requester may read their own request and nothing more
    Given "m.alvarez" filed "PRR-2026-0100" through the public portal
    Then "m.alvarez" may "read" "PRR-2026-0100"
    But "m.alvarez" may not "acknowledge" "PRR-2026-0100"
    And "m.alvarez" may not "release" "PRR-2026-0100"

  Scenario: A requester may not read somebody else's request
    Given "m.alvarez" filed "PRR-2026-0100" through the public portal
    Then "m.alvarez" may not "read" "PRR-2026-0041"

  Scenario: Only the fulfillment service may report a delivery outcome
    Given request "PRR-2026-0041" has a release awaiting delivery
    Then the fulfillment service may "record_delivery" "PRR-2026-0041"
    But "r.okafor" may not "record_delivery" "PRR-2026-0041"
    And "c.hall" may not "record_delivery" "PRR-2026-0041"

  Scenario: An officer cannot compensate their own release
    Given request "PRR-2026-0041" has a release awaiting delivery
    When "r.okafor" reports that the package for "PRR-2026-0041" failed because "wrong address"
    Then the command is refused because the caller is not authorized
    And request "PRR-2026-0041" is "release_pending"

  Scenario: The list a caller sees holds only what they may read
    Given "m.alvarez" filed "PRR-2026-0100" through the public portal
    Then "r.okafor" sees 2 requests
    And "m.alvarez" sees 1 request
    And "t.nowak" sees 0 requests
