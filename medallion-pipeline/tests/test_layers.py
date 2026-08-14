"""Layer boundaries: bronze is lossless, gold comes from silver, and it adds up."""

from __future__ import annotations

import math
from pathlib import Path

import pytest

from pipeline import bronze, gold
from pipeline.errors import ReconciliationError, SchemaMismatchError, SourceNotFoundError
from pipeline.rules import KNOWN_BOROUGHS
from tests.conftest import SAMPLE_CSV, record

SAMPLE_ROW_COUNT = 140


def test_bronze_rejects_nothing(sample_bronze):
    """Every line of the source file becomes a bronze row, malformed or not."""
    assert len(sample_bronze) == SAMPLE_ROW_COUNT
    assert (sample_bronze["tpep_dropoff_datetime"] == "not recorded").sum() == 1
    assert (sample_bronze["tpep_pickup_datetime"] == "").sum() == 1
    assert sample_bronze["_source_file"].nunique() == 1
    assert sample_bronze["_source_row"].tolist() == list(range(2, SAMPLE_ROW_COUNT + 2))


def test_bronze_retains_a_row_that_silver_quarantines(sample_bronze, sample_silver):
    """The record silver refuses is still on disk, verbatim, in bronze."""
    negative_fare = sample_bronze.loc[sample_bronze["fare_amount"] == "-12.50"]
    assert len(negative_fare) == 1
    source_row = int(negative_fare.iloc[0]["_source_row"])

    assert source_row not in set(sample_silver.clean["_source_row"])

    held = sample_silver.quarantine.loc[sample_silver.quarantine["_source_row"] == source_row]
    assert len(held) == 1
    assert held.iloc[0]["_failed_rules"] == "fare_amount_non_negative"
    # The quarantine row carries the original text, not a cleaned-up version.
    assert held.iloc[0]["fare_amount"] == "-12.50"


def test_silver_accounts_for_every_bronze_row(sample_bronze, sample_silver):
    """Clean plus quarantined equals ingested. Nothing evaporates in between."""
    assert sample_silver.rows_in == len(sample_bronze)
    assert len(sample_silver.quarantine) == 14
    assert len(sample_silver.clean) == SAMPLE_ROW_COUNT - 14


def test_a_clean_record_survives_all_three_layers(run_layers):
    """One good record and one bad one: only the good one reaches the briefing."""
    raw, result = run_layers([record(), record(fare_amount="-9.99")])

    assert len(raw) == 2
    assert len(result.clean) == 1
    assert len(result.quarantine) == 1

    published = gold.publish(result.clean)
    assert len(published) == 1
    row = published.iloc[0]
    assert row["trip_date"] == "2026-03-02"
    assert row["pickup_borough"] == "Manhattan"
    assert row["trips"] == 1
    assert row["average_fare"] == pytest.approx(24.60)
    assert row["total_fare"] == pytest.approx(24.60)


def test_gold_is_built_from_silver_only(sample_silver):
    """No quarantined value can appear in the published aggregate."""
    published = gold.publish(sample_silver.clean)
    assert set(published["pickup_borough"]) <= KNOWN_BOROUGHS
    assert (published["total_fare"] >= 0).all()
    assert published["trips"].sum() == len(sample_silver.clean)


def test_gold_totals_equal_silver_totals(sample_silver):
    """The reconciliation that makes the numbers defensible."""
    published = gold.publish(sample_silver.clean)

    assert int(published["trips"].sum()) == len(sample_silver.clean)
    assert math.isclose(
        float(published["total_fare"].sum()),
        float(sample_silver.clean["fare_amount"].sum()),
        abs_tol=0.01 * len(published),
    )
    gold.reconcile(sample_silver.clean, published)  # must not raise


def test_reconciliation_catches_a_lost_row(sample_silver):
    published = gold.publish(sample_silver.clean)
    with pytest.raises(ReconciliationError, match="trips"):
        gold.reconcile(sample_silver.clean, published.iloc[1:])


def test_reconciliation_catches_a_changed_total(sample_silver):
    published = gold.publish(sample_silver.clean)
    published.loc[0, "total_fare"] = published.loc[0, "total_fare"] + 50.0
    with pytest.raises(ReconciliationError, match="fare total"):
        gold.reconcile(sample_silver.clean, published)


def test_missing_source_is_a_named_failure(tmp_path: Path):
    with pytest.raises(SourceNotFoundError):
        bronze.ingest_csv(tmp_path / "absent.csv")


def test_renamed_column_is_a_named_failure(tmp_path: Path):
    text = SAMPLE_CSV.read_text(encoding="utf-8").replace("fare_amount", "fare", 1)
    path = tmp_path / "renamed.csv"
    path.write_text(text, encoding="utf-8")
    with pytest.raises(SchemaMismatchError, match="fare_amount"):
        bronze.ingest_csv(path)
