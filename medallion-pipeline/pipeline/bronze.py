"""Bronze layer: raw ingestion, schema on read.

Bronze rejects nothing. Every line of the source file becomes a bronze row
exactly as it was written -- every field is read as text, so a malformed
timestamp or an empty required field survives instead of being coerced away at
the door. What bronze adds is the provenance a later question needs: which file
a row came from, which line of it, when it was ingested, and a content hash that
identifies the record independent of its position.

That is the whole point of the layer: when silver quarantines a row, the
original is still on disk, untouched, and can be shown to whoever sent it.
"""

from __future__ import annotations

import hashlib
from datetime import datetime, timezone
from pathlib import Path

import pandas as pd

from .errors import SchemaMismatchError, SourceNotFoundError

#: Columns the source extract must carry. Named after the published NYC TLC
#: yellow-taxi trip record dictionary so the shape is recognizable; the sample
#: file in ``data/`` is synthetic.
SOURCE_COLUMNS: tuple[str, ...] = (
    "vendor_id",
    "tpep_pickup_datetime",
    "tpep_dropoff_datetime",
    "passenger_count",
    "trip_distance",
    "pickup_borough",
    "payment_type",
    "fare_amount",
    "tip_amount",
    "total_amount",
)

#: Provenance columns bronze adds. Underscore-prefixed so they never collide
#: with a source column, and carried unchanged through silver and quarantine.
METADATA_COLUMNS: tuple[str, ...] = (
    "_source_file",
    "_source_row",
    "_ingested_at",
    "_row_hash",
)


def row_hash(values: tuple[str, ...]) -> str:
    """Return a stable content hash for one record's source values.

    The hash covers the source fields only -- never the ingestion metadata -- so
    the same record re-sent in a later file hashes the same and can be
    recognized as a repeat.
    """
    joined = "\x1f".join(values)
    return hashlib.sha256(joined.encode("utf-8")).hexdigest()[:16]


def ingest_csv(path: str | Path, ingested_at: datetime | None = None) -> pd.DataFrame:
    """Read a source CSV into a bronze table.

    Args:
        path: The source extract to ingest.
        ingested_at: Ingestion timestamp to stamp on every row. Defaults to now
            in UTC; injectable so tests and replays are deterministic.

    Returns:
        A frame of the source columns as text, plus the metadata columns.

    Raises:
        SourceNotFoundError: The file does not exist.
        SchemaMismatchError: The file is missing a required source column.
    """
    source = Path(path)
    if not source.is_file():
        raise SourceNotFoundError(f"source file not found: {source}")

    # dtype=str with keep_default_na=False is what makes this schema on read:
    # nothing is parsed, nothing is inferred, and an empty field stays an empty
    # string rather than becoming a null that hides whether it was ever sent.
    frame = pd.read_csv(source, dtype=str, keep_default_na=False)

    missing = [column for column in SOURCE_COLUMNS if column not in frame.columns]
    if missing:
        raise SchemaMismatchError(f"{source} is missing required column(s): {', '.join(missing)}")

    frame = frame.loc[:, list(SOURCE_COLUMNS)].copy()
    stamp = ingested_at or datetime.now(timezone.utc)

    frame["_source_file"] = source.name
    frame["_source_row"] = range(2, len(frame) + 2)  # 1-based, header is line 1
    frame["_ingested_at"] = stamp.isoformat()
    frame["_row_hash"] = [
        row_hash(tuple(values)) for values in frame[list(SOURCE_COLUMNS)].to_numpy()
    ]
    return frame
