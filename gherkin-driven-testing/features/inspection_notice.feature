Feature: Notice a contractor must give before an inspection is booked

  In order to route a certified inspector to a site that is ready for one
  As a permit services scheduler
  I want the notice a request must give to follow from the priority of the work
  So that a life-safety condition is seen the same day without letting routine
  work jump ahead of contractors who planned properly

  Background:
    Given the inspection calendar observes "Independence Day" on 2026-07-03
    And the "Northgate" district staffs 4 routine slots and 1 reserve slot each day
    And permit "BP-4417" is active in the "Northgate" district

  # 2026-06-15 is a Monday and 2026-06-30 is a Tuesday. Every date below is
  # written out rather than computed, because a criterion an inspector cannot
  # check by looking at a wall calendar is not a criterion they agreed to.

  Scenario Outline: Notice given is enough, so the inspection is booked
    When a "<priority>" inspection for permit "BP-4417" is filed on <filed> for <wanted>
    Then the inspection is booked

    Examples: the earliest day each priority allows
      | priority  | filed      | wanted     |
      | standard  | 2026-06-15 | 2026-06-18 |
      | expedited | 2026-06-15 | 2026-06-16 |
      | emergency | 2026-06-15 | 2026-06-15 |

    Examples: days the office is closed are not notice
      | priority | filed      | wanted     |
      | standard | 2026-06-18 | 2026-06-23 |
      | standard | 2026-06-30 | 2026-07-06 |

  Scenario Outline: Notice given is one day short, so the request is refused
    When a "<priority>" inspection for permit "BP-4417" is filed on <filed> for <wanted>
    Then the request is refused because "notice period is too short"

    Examples: one business day inside the boundary
      | priority  | filed      | wanted     |
      | standard  | 2026-06-15 | 2026-06-17 |
      | expedited | 2026-06-15 | 2026-06-15 |

    Examples: the weekend and the observed holiday do not close the gap
      | priority | filed      | wanted     |
      | standard | 2026-06-18 | 2026-06-22 |
      | standard | 2026-06-30 | 2026-07-02 |

  Scenario: An emergency is still refused on a day the office is closed
    When an "emergency" inspection for permit "BP-4417" is filed on 2026-07-03 for 2026-07-03
    Then the request is refused because "requested date is not a business day"

  Scenario: A suspended permit is refused before the calendar is consulted
    Given permit "BP-4420" is suspended in the "Northgate" district
    When a "standard" inspection for permit "BP-4420" is filed on 2026-06-15 for 2026-07-04
    Then the request is refused because "permit is not active"

  Scenario: A day already past is refused whatever the priority
    When an "emergency" inspection for permit "BP-4417" is filed on 2026-06-18 for 2026-06-17
    Then the request is refused because "requested date has already passed"

  Scenario: A site outside the inspection area is refused
    Given permit "BP-5100" is active in the "Riverbend" district
    When a "standard" inspection for permit "BP-5100" is filed on 2026-06-15 for 2026-06-18
    Then the request is refused because "district is outside the inspection area"
