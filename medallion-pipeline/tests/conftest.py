"""Shared fixtures: a known-good record, a CSV writer, and the sample batch."""

from __future__ import annotations

import csv
from datetime import datetime, timezone
from pathlib import Path

import pytest

from pipeline import bronze, silver

#: The committed sample extract the entry point runs against.
SAMPLE_CSV = Path(__file__).parent.parent / "data" / "taxi_trips_sample.csv"

#: Fixed timestamps so every layer in a test run is reproducible.
INGESTED_AT = datetime(2026, 3, 9, 6, 0, 0, tzinfo=timezone.utc)
QUARANTINED_AT = datetime(2026, 3, 9, 6, 0, 5, tzinfo=timezone.utc)

#: One record that satisfies every rule. Tests break exactly one field of it at
#: a time, so a quarantine reason can only come from the field that was broken.
GOOD_RECORD: dict[str, str] = {
    "vendor_id": "2",
    "tpep_pickup_datetime": "2026-03-02 08:14:00",
    "tpep_dropoff_datetime": "2026-03-02 08:41:00",
    "passenger_count": "2",
    "trip_distance": "4.20",
    "pickup_borough": "Manhattan",
    "payment_type": "credit_card",
    "fare_amount": "24.60",
    "tip_amount": "5.10",
    "total_amount": "33.50",
}


def record(**overrides: str) -> dict[str, str]:
    """Return the known-good record with specific fields replaced."""
    return {**GOOD_RECORD, **overrides}


@pytest.fixture
def write_csv(tmp_path: Path):
    """Return a helper that writes records to a CSV and returns its path."""

    def _write(records: list[dict[str, str]], name: str = "batch.csv") -> Path:
        path = tmp_path / name
        with path.open("w", newline="", encoding="utf-8") as handle:
            writer = csv.DictWriter(handle, fieldnames=list(bronze.SOURCE_COLUMNS))
            writer.writeheader()
            writer.writerows(records)
        return path

    return _write


@pytest.fixture
def run_layers(write_csv):
    """Return a helper that takes records through bronze and silver."""

    def _run(records: list[dict[str, str]]) -> tuple:
        raw = bronze.ingest_csv(write_csv(records), ingested_at=INGESTED_AT)
        return raw, silver.refine(raw, quarantined_at=QUARANTINED_AT)

    return _run


@pytest.fixture(scope="session")
def sample_bronze():
    """The committed sample extract, ingested."""
    return bronze.ingest_csv(SAMPLE_CSV, ingested_at=INGESTED_AT)


@pytest.fixture(scope="session")
def sample_silver(sample_bronze):
    """The committed sample extract, refined and split."""
    return silver.refine(sample_bronze, quarantined_at=QUARANTINED_AT)
