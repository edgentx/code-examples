"""Gold layer: the small aggregate a program office actually reads.

Trips, miles, and average fare by service day and pickup borough. It is built
from clean silver rows only -- never from bronze -- so every number in it has
already passed the rule set, and the rows that did not are sitting in quarantine
with their reasons instead of being averaged in here.

The layer ships its own reconciliation. An aggregate is only defensible if it
still adds up to its input, so :func:`reconcile` re-derives the silver totals
from the published table and refuses the run if they disagree.
"""

from __future__ import annotations

import math

import pandas as pd

from .errors import ReconciliationError

#: Money is carried as a float here for brevity, so per-group rounding can drift
#: by up to half a cent. Reconciliation allows one cent per published group and
#: nothing more.
CENT = 0.01


def publish(silver: pd.DataFrame) -> pd.DataFrame:
    """Aggregate clean silver rows into the briefing table.

    Args:
        silver: The clean output of :func:`pipeline.silver.refine`.

    Returns:
        One row per service day and pickup borough, sorted for reading.
    """
    if silver.empty:
        return pd.DataFrame(
            columns=[
                "trip_date",
                "pickup_borough",
                "trips",
                "total_miles",
                "total_fare",
                "average_fare",
            ]
        )

    aggregate = silver.groupby(["trip_date", "pickup_borough"], as_index=False).agg(
        trips=("_row_hash", "size"),
        total_miles=("trip_distance", "sum"),
        total_fare=("fare_amount", "sum"),
    )
    aggregate["average_fare"] = (aggregate["total_fare"] / aggregate["trips"]).round(2)
    aggregate["total_miles"] = aggregate["total_miles"].round(2)
    aggregate["total_fare"] = aggregate["total_fare"].round(2)
    return aggregate.sort_values(["trip_date", "pickup_borough"], ignore_index=True)


def reconcile(silver: pd.DataFrame, gold: pd.DataFrame) -> None:
    """Check that the published aggregate accounts for every silver row.

    Raises:
        ReconciliationError: The gold trip count or fare total does not match
            the silver rows it was built from, which means rows were invented or
            lost between the layers.
    """
    published_trips = int(gold["trips"].sum()) if not gold.empty else 0
    if published_trips != len(silver):
        raise ReconciliationError(
            f"gold publishes {published_trips} trips, silver holds {len(silver)}"
        )

    published_fare = float(gold["total_fare"].sum()) if not gold.empty else 0.0
    silver_fare = float(silver["fare_amount"].sum()) if not silver.empty else 0.0
    tolerance = CENT * max(len(gold), 1)
    if not math.isclose(published_fare, silver_fare, abs_tol=tolerance):
        raise ReconciliationError(
            f"gold fare total {published_fare:.2f} differs from silver fare total "
            f"{silver_fare:.2f} by more than {tolerance:.2f}"
        )
