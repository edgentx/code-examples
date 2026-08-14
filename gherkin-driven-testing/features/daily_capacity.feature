Feature: Daily inspection capacity in a district

  In order to keep every appointment the division has already promised
  As a permit services scheduler
  I want a district's day to stop taking routine work once its inspectors are committed
  So that a life-safety condition can still be seen on a day that is otherwise full

  Background:
    Given the "Northgate" district staffs 2 routine slots and 1 reserve slot each day
    And permit "BP-4417" is active in the "Northgate" district

  Scenario: The last routine appointment of the day is honored
    Given 1 routine inspection is already booked in "Northgate" on 2026-06-18
    When a "standard" inspection for permit "BP-4417" is filed on 2026-06-15 for 2026-06-18
    Then the inspection is booked
    And the booking uses a routine slot

  Scenario: A day with every routine appointment committed refuses more routine work
    Given 2 routine inspections are already booked in "Northgate" on 2026-06-18
    When a "standard" inspection for permit "BP-4417" is filed on 2026-06-15 for 2026-06-18
    Then the request is refused because "no inspection slot is available"

  Scenario: An emergency takes the reserve appointment on a full day
    Given 2 routine inspections are already booked in "Northgate" on 2026-06-18
    When an "emergency" inspection for permit "BP-4417" is filed on 2026-06-18 for 2026-06-18
    Then the inspection is booked
    And the booking uses a reserve slot

  Scenario: The reserve appointment is good for one emergency only
    Given 2 routine inspections are already booked in "Northgate" on 2026-06-18
    And 1 emergency inspection is already booked in "Northgate" on 2026-06-18
    When an "emergency" inspection for permit "BP-4417" is filed on 2026-06-18 for 2026-06-18
    Then the request is refused because "no inspection slot is available"

  Scenario: Paying to expedite does not buy the reserve appointment
    Given 2 routine inspections are already booked in "Northgate" on 2026-06-18
    When an "expedited" inspection for permit "BP-4417" is filed on 2026-06-16 for 2026-06-18
    Then the request is refused because "no inspection slot is available"

  Scenario: A full day does not close the day after it
    Given 2 routine inspections are already booked in "Northgate" on 2026-06-18
    When a "standard" inspection for permit "BP-4417" is filed on 2026-06-15 for 2026-06-19
    Then the inspection is booked

  Scenario: Notice is settled before capacity, so the contractor hears the rule they can act on
    Given 2 routine inspections are already booked in "Northgate" on 2026-06-17
    When a "standard" inspection for permit "BP-4417" is filed on 2026-06-15 for 2026-06-17
    Then the request is refused because "notice period is too short"
