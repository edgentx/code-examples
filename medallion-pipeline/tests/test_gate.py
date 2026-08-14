"""The gate: publish a batch with some bad rows, refuse a broken extract."""

from __future__ import annotations

import pytest

from pipeline.errors import QualityGateError
from pipeline.quality import (
    THRESHOLD_ENV_VAR,
    QualityGate,
    quarantine_reason_counts,
    warning_counts,
)

# The sample batch quarantines 14 of 140 rows: exactly one row in ten.
SAMPLE_FRACTION = 0.10


@pytest.mark.parametrize("threshold", [0.0, 0.01, 0.05, 0.099])
def test_gate_stops_a_batch_over_the_threshold(sample_silver, threshold):
    gate = QualityGate(max_quarantine_fraction=threshold)
    with pytest.raises(QualityGateError) as raised:
        gate.enforce(sample_silver)

    failure = raised.value
    assert failure.quarantined == 14
    assert failure.total == 140
    assert failure.fraction == pytest.approx(SAMPLE_FRACTION)
    assert failure.threshold == threshold
    assert "quality gate failed" in str(failure)


@pytest.mark.parametrize("threshold", [0.10, 0.15, 0.5, 1.0])
def test_gate_allows_a_batch_under_the_threshold(sample_silver, threshold):
    report = QualityGate(max_quarantine_fraction=threshold).enforce(sample_silver)

    assert report.passed
    assert report.published == 126
    assert report.fraction == pytest.approx(SAMPLE_FRACTION)


def test_assess_reports_without_raising(sample_silver):
    """Assessment is separable from enforcement, so a run can print the numbers
    that are about to stop it."""
    report = QualityGate(max_quarantine_fraction=0.01).assess(sample_silver)
    assert not report.passed
    assert report.quarantined == 14


@pytest.mark.parametrize("threshold", [-0.1, 1.5])
def test_a_nonsensical_threshold_is_refused(threshold):
    with pytest.raises(ValueError, match="between 0 and 1"):
        QualityGate(max_quarantine_fraction=threshold)


def test_threshold_can_be_overridden_by_the_environment():
    assert QualityGate.from_env({}).max_quarantine_fraction == pytest.approx(0.15)
    tightened = QualityGate.from_env({THRESHOLD_ENV_VAR: "0.02"})
    assert tightened.max_quarantine_fraction == pytest.approx(0.02)
    with pytest.raises(ValueError, match=THRESHOLD_ENV_VAR):
        QualityGate.from_env({THRESHOLD_ENV_VAR: "a third"})


def test_reason_counts_name_every_rule_that_fired(sample_silver):
    counts = quarantine_reason_counts(sample_silver.quarantine)

    # Fourteen rows, fifteen reasons: one record failed two rules at once.
    assert sum(counts.values()) == 15
    assert counts["fare_amount_non_negative"] == 2
    assert counts["passenger_count_in_range"] == 3
    assert counts["unique_trip_record"] == 1
    assert warning_counts(sample_silver.clean) == {"trip_distance_plausible": 2}
