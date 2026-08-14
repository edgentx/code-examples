"""The data-quality rule set.

Rules are data, not control flow. Each one is a named object carrying the
predicate, a severity, and a plain-language description, and the whole set is a
list that can be read top to bottom by someone who does not write Python. Adding
a rule means appending an entry here; nothing in the silver transform changes.

Two severities, two behaviors:

* ``REJECT`` -- the row is quarantined and never reaches gold.
* ``WARN``   -- the row is published, but the warning travels with it, so an
  implausible value can be reviewed without being deleted first.

Predicates answer "is this row acceptable on this one dimension" and nothing
else. A predicate that cannot apply -- a range check on a field that was never
sent -- returns ``True`` and lets the rule that *does* apply own the failure.
That is what keeps a quarantine reason precise enough to send back to the
originating office.
"""

from __future__ import annotations

from collections.abc import Callable, Mapping, Sequence
from dataclasses import dataclass
from enum import Enum
from typing import Any

import pandas as pd

Row = Mapping[str, Any]
Predicate = Callable[[Row], bool]

#: Boroughs a pickup zone can resolve to. Real TLC extracts carry a numeric
#: location identifier that joins to a zone lookup; anything that fails to
#: resolve arrives as an unrecognized value, which is exactly what this
#: allow-list catches before it can smear into a borough total.
KNOWN_BOROUGHS: frozenset[str] = frozenset(
    {"Manhattan", "Brooklyn", "Queens", "Bronx", "Staten Island", "EWR"}
)

#: Yellow-taxi vehicles seat six. Zero passengers and nine passengers are both
#: meter-entry errors, not trips.
MIN_PASSENGERS = 1
MAX_PASSENGERS = 6

#: Miles beyond which a single metered trip is treated as implausible.
MAX_PLAUSIBLE_MILES = 100.0


class Severity(str, Enum):
    """What a failed rule does to the row that failed it."""

    REJECT = "reject"
    WARN = "warn"


@dataclass(frozen=True)
class Rule:
    """One named quality expectation applied to one row."""

    name: str
    severity: Severity
    description: str
    predicate: Predicate

    def holds(self, row: Row) -> bool:
        """Return True when the row satisfies this rule."""
        return self.predicate(row)


def is_missing(value: Any) -> bool:
    """Return True when a value was never populated.

    Treats an empty or whitespace-only string as missing, because a CSV cannot
    distinguish "not sent" from "sent empty" and neither can be used.
    """
    if isinstance(value, str):
        return value.strip() == ""
    return bool(pd.isna(value))


def _both_timestamps_parsed(row: Row) -> bool:
    return not is_missing(row["pickup_datetime"]) and not is_missing(row["dropoff_datetime"])


RULES: tuple[Rule, ...] = (
    Rule(
        name="pickup_timestamp_present",
        severity=Severity.REJECT,
        description="Pickup timestamp is populated.",
        predicate=lambda row: not is_missing(row["_raw_pickup_datetime"]),
    ),
    Rule(
        name="dropoff_timestamp_present",
        severity=Severity.REJECT,
        description="Dropoff timestamp is populated.",
        predicate=lambda row: not is_missing(row["_raw_dropoff_datetime"]),
    ),
    Rule(
        name="pickup_timestamp_valid",
        severity=Severity.REJECT,
        description="Pickup timestamp, when populated, parses as a real date and time.",
        predicate=lambda row: is_missing(row["_raw_pickup_datetime"])
        or not is_missing(row["pickup_datetime"]),
    ),
    Rule(
        name="dropoff_timestamp_valid",
        severity=Severity.REJECT,
        description="Dropoff timestamp, when populated, parses as a real date and time.",
        predicate=lambda row: is_missing(row["_raw_dropoff_datetime"])
        or not is_missing(row["dropoff_datetime"]),
    ),
    Rule(
        name="dropoff_after_pickup",
        severity=Severity.REJECT,
        description="The trip ends after it starts.",
        predicate=lambda row: not _both_timestamps_parsed(row)
        or row["dropoff_datetime"] > row["pickup_datetime"],
    ),
    Rule(
        name="fare_amount_present",
        severity=Severity.REJECT,
        description="Fare amount is populated and numeric.",
        predicate=lambda row: not is_missing(row["fare_amount"]),
    ),
    Rule(
        name="fare_amount_non_negative",
        severity=Severity.REJECT,
        description="Fare amount is not negative. Refunds belong in an adjustment feed.",
        predicate=lambda row: is_missing(row["fare_amount"]) or row["fare_amount"] >= 0,
    ),
    Rule(
        name="passenger_count_in_range",
        severity=Severity.REJECT,
        description=f"Passenger count is between {MIN_PASSENGERS} and {MAX_PASSENGERS}.",
        predicate=lambda row: not is_missing(row["passenger_count"])
        and MIN_PASSENGERS <= row["passenger_count"] <= MAX_PASSENGERS,
    ),
    Rule(
        name="pickup_borough_known",
        severity=Severity.REJECT,
        description="Pickup borough resolves to a known service area.",
        predicate=lambda row: row["pickup_borough"] in KNOWN_BOROUGHS,
    ),
    Rule(
        name="unique_trip_record",
        severity=Severity.REJECT,
        description="The record has not already appeared in this batch.",
        predicate=lambda row: not row["_is_duplicate"],
    ),
    Rule(
        name="trip_distance_plausible",
        severity=Severity.WARN,
        description=f"Trip distance is at most {MAX_PLAUSIBLE_MILES:.0f} miles.",
        predicate=lambda row: is_missing(row["trip_distance"])
        or row["trip_distance"] <= MAX_PLAUSIBLE_MILES,
    ),
)


def rules_by_severity(severity: Severity, rules: Sequence[Rule] = RULES) -> tuple[Rule, ...]:
    """Return the rules carrying a given severity, in declaration order."""
    return tuple(rule for rule in rules if rule.severity is severity)


def evaluate(row: Row, rules: Sequence[Rule] = RULES) -> dict[Severity, list[str]]:
    """Apply every rule to one row.

    Returns:
        The names of the failed rules, grouped by severity and kept in
        declaration order. A row that fails nothing yields empty lists.
    """
    failures: dict[Severity, list[str]] = {severity: [] for severity in Severity}
    for rule in rules:
        if not rule.holds(row):
            failures[rule.severity].append(rule.name)
    return failures
