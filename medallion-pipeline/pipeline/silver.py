"""Silver layer: typed, deduplicated, validated -- and quarantined.

Silver is where the batch is split in two. Rows that satisfy every rejecting
rule become the clean silver table, the only input gold is ever built from.
Rows that fail one or more become quarantine rows: the *original* bronze record,
verbatim, plus the names of every rule it failed and the time it was set aside.

Quarantine is a table written to disk beside the clean data, not a log line and
not a dropped row. A row that nobody can explain is a question someone will ask
later, and the answer has to be retrievable by whoever asks.

Timestamps are expected in the source format the extract publishes,
``YYYY-MM-DD HH:MM:SS``; anything else fails the parse rules rather than being
guessed at.
"""

from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime, timezone

import pandas as pd

from .bronze import METADATA_COLUMNS, SOURCE_COLUMNS
from .rules import RULES, Row, Rule, Severity, evaluate

#: The one timestamp format the source extract is contracted to publish.
TIMESTAMP_FORMAT = "%Y-%m-%d %H:%M:%S"

#: Columns silver needs while rules run but does not publish.
_WORKING_COLUMNS: tuple[str, ...] = (
    "_raw_pickup_datetime",
    "_raw_dropoff_datetime",
    "_is_duplicate",
)


@dataclass(frozen=True)
class SilverResult:
    """The two outputs of the silver transform."""

    clean: pd.DataFrame
    quarantine: pd.DataFrame

    @property
    def rows_in(self) -> int:
        """Total rows considered, which must equal the bronze row count."""
        return len(self.clean) + len(self.quarantine)


def cast_types(bronze: pd.DataFrame) -> pd.DataFrame:
    """Convert bronze text into typed values without discarding anything.

    Values that cannot be converted become null rather than raising, so the rule
    set -- not the parser -- decides what happens to a bad record. The raw text
    of each timestamp is carried alongside so a rule can still tell "never sent"
    apart from "sent unparseable".
    """
    typed = pd.DataFrame(index=bronze.index)
    typed["vendor_id"] = bronze["vendor_id"]
    typed["pickup_datetime"] = pd.to_datetime(
        bronze["tpep_pickup_datetime"], format=TIMESTAMP_FORMAT, errors="coerce"
    )
    typed["dropoff_datetime"] = pd.to_datetime(
        bronze["tpep_dropoff_datetime"], format=TIMESTAMP_FORMAT, errors="coerce"
    )
    numeric = ("passenger_count", "trip_distance", "fare_amount", "tip_amount", "total_amount")
    for column in numeric:
        typed[column] = pd.to_numeric(bronze[column], errors="coerce")
    typed["pickup_borough"] = bronze["pickup_borough"]
    typed["payment_type"] = bronze["payment_type"]

    for column in METADATA_COLUMNS:
        typed[column] = bronze[column]

    typed["_raw_pickup_datetime"] = bronze["tpep_pickup_datetime"]
    typed["_raw_dropoff_datetime"] = bronze["tpep_dropoff_datetime"]
    # First occurrence wins; every later copy of the same content hash is the
    # duplicate. Marking it here rather than dropping it means the repeat is
    # quarantined with a reason instead of vanishing.
    typed["_is_duplicate"] = bronze["_row_hash"].duplicated(keep="first")
    return typed


def refine(
    bronze: pd.DataFrame,
    rules: tuple[Rule, ...] = RULES,
    quarantined_at: datetime | None = None,
) -> SilverResult:
    """Type, deduplicate, and validate a bronze batch.

    Args:
        bronze: The output of :func:`pipeline.bronze.ingest_csv`.
        rules: The rule set to apply. Defaults to the full set.
        quarantined_at: Timestamp stamped on quarantine rows. Defaults to now in
            UTC; injectable so tests are deterministic.

    Returns:
        A :class:`SilverResult` holding the clean table and the quarantine
        table. Together they account for every bronze row exactly once.
    """
    typed = cast_types(bronze)
    records: list[Row] = typed.to_dict("records")

    rejected_names: list[str] = []
    warning_names: list[str] = []
    for record in records:
        failures = evaluate(record, rules)
        rejected_names.append(", ".join(failures[Severity.REJECT]))
        warning_names.append(", ".join(failures[Severity.WARN]))

    rejected = pd.Series(rejected_names, index=typed.index) != ""

    clean = typed.loc[~rejected].drop(columns=list(_WORKING_COLUMNS)).copy()
    clean["passenger_count"] = clean["passenger_count"].astype("Int64")
    clean["_warnings"] = pd.Series(warning_names, index=typed.index).loc[~rejected]
    clean["trip_date"] = clean["pickup_datetime"].dt.date.astype(str)

    stamp = quarantined_at or datetime.now(timezone.utc)
    quarantine = bronze.loc[rejected, list(SOURCE_COLUMNS) + list(METADATA_COLUMNS)].copy()
    quarantine["_failed_rules"] = pd.Series(rejected_names, index=typed.index).loc[rejected]
    quarantine["_quarantined_at"] = stamp.isoformat()

    return SilverResult(
        clean=clean.reset_index(drop=True),
        quarantine=quarantine.reset_index(drop=True),
    )
