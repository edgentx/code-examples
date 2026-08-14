"""Every rejecting rule, proved one at a time.

Each case is a batch built from the known-good record with a single field
broken. The assertion is not only that the row was held back -- it is that the
quarantine row names the rule that held it back, because "one row was dropped"
is not an answer anyone can act on.
"""

from __future__ import annotations

import pandas as pd
import pytest

from pipeline.rules import RULES, Severity, evaluate, rules_by_severity
from tests.conftest import GOOD_RECORD, record

# (rule name, the batch that should trip it). Every batch is one record except
# the duplicate case, which needs the same record twice.
CASES: list[tuple[str, list[dict[str, str]]]] = [
    ("pickup_timestamp_present", [record(tpep_pickup_datetime="")]),
    ("dropoff_timestamp_present", [record(tpep_dropoff_datetime="")]),
    ("pickup_timestamp_valid", [record(tpep_pickup_datetime="2026-03-32 08:14:00")]),
    ("dropoff_timestamp_valid", [record(tpep_dropoff_datetime="not recorded")]),
    ("dropoff_after_pickup", [record(tpep_dropoff_datetime="2026-03-02 07:55:00")]),
    ("fare_amount_present", [record(fare_amount="")]),
    ("fare_amount_non_negative", [record(fare_amount="-24.60")]),
    ("passenger_count_in_range", [record(passenger_count="0")]),
    ("passenger_count_in_range", [record(passenger_count="9")]),
    ("passenger_count_in_range", [record(passenger_count="")]),
    ("pickup_borough_known", [record(pickup_borough="Manhatan")]),
    ("pickup_borough_known", [record(pickup_borough="Unknown")]),
    ("unique_trip_record", [record(), record()]),
]


@pytest.mark.parametrize(
    "rule_name, records",
    CASES,
    ids=[f"{name}-{index}" for index, (name, _) in enumerate(CASES)],
)
def test_offending_row_is_quarantined_with_its_reason(rule_name, records, run_layers):
    _, result = run_layers(records)

    assert len(result.quarantine) == 1, "exactly one record should have been set aside"
    assert result.quarantine.loc[0, "_failed_rules"] == rule_name
    assert result.quarantine.loc[0, "_quarantined_at"].startswith("2026-03-09T06:00:05")
    assert len(result.clean) == len(records) - 1


def test_every_rejecting_rule_has_a_case():
    """A rule added without a test would otherwise be silently unproved."""
    covered = {name for name, _ in CASES}
    declared = {rule.name for rule in rules_by_severity(Severity.REJECT)}
    assert covered == declared


def test_a_row_failing_two_rules_carries_both_reasons(run_layers):
    _, result = run_layers(
        [record(fare_amount="-4.80", tpep_dropoff_datetime="2026-03-02 08:08:00")]
    )

    assert len(result.quarantine) == 1
    reasons = result.quarantine.loc[0, "_failed_rules"].split(", ")
    assert set(reasons) == {"dropoff_after_pickup", "fare_amount_non_negative"}


def test_a_warning_publishes_the_row_and_travels_with_it(run_layers):
    """A WARN rule flags the row instead of holding it back."""
    _, result = run_layers([record(trip_distance="212.40")])

    assert result.quarantine.empty
    assert len(result.clean) == 1
    assert result.clean.loc[0, "_warnings"] == "trip_distance_plausible"


def test_the_known_good_record_fails_nothing():
    row = {
        "_raw_pickup_datetime": GOOD_RECORD["tpep_pickup_datetime"],
        "_raw_dropoff_datetime": GOOD_RECORD["tpep_dropoff_datetime"],
        "pickup_datetime": pd.Timestamp(GOOD_RECORD["tpep_pickup_datetime"]),
        "dropoff_datetime": pd.Timestamp(GOOD_RECORD["tpep_dropoff_datetime"]),
        "passenger_count": 2.0,
        "trip_distance": 4.2,
        "fare_amount": 24.6,
        "pickup_borough": "Manhattan",
        "_is_duplicate": False,
    }
    failures = evaluate(row, RULES)
    assert failures[Severity.REJECT] == []
    assert failures[Severity.WARN] == []
