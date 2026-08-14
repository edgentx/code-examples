"""The quality gate.

Quarantining bad rows is only half the control. The other half is refusing to
publish at all when too much of a batch is bad, because a gold table built from
the surviving 60% of a broken extract looks exactly like a healthy one -- same
columns, plausible numbers, no error anywhere -- and it will be briefed as if it
were complete.

So the gate is a threshold on the quarantined share of the batch. Under it, the
run publishes and the quarantine table is the follow-up work. Over it, the run
fails with :class:`~pipeline.errors.QualityGateError` and gold is left holding
its previous contents rather than a partial refresh.
"""

from __future__ import annotations

import os
from collections import Counter
from dataclasses import dataclass

import pandas as pd

from .errors import QualityGateError
from .silver import SilverResult

#: Environment variable that overrides the configured threshold, so the gate can
#: be tightened for a run without a code change.
THRESHOLD_ENV_VAR = "MEDALLION_MAX_QUARANTINE_FRACTION"

#: Default: up to fifteen rows in a hundred may be quarantined before the run is
#: treated as a broken extract rather than a batch with some bad records in it.
DEFAULT_MAX_QUARANTINE_FRACTION = 0.15


@dataclass(frozen=True)
class GateReport:
    """The arithmetic behind a gate decision, whichever way it went."""

    total: int
    quarantined: int
    threshold: float

    @property
    def published(self) -> int:
        """Rows that passed every rejecting rule."""
        return self.total - self.quarantined

    @property
    def fraction(self) -> float:
        """Quarantined share of the batch. An empty batch quarantines nothing."""
        return self.quarantined / self.total if self.total else 0.0

    @property
    def passed(self) -> bool:
        """True when the quarantined share is within the threshold."""
        return self.fraction <= self.threshold


@dataclass(frozen=True)
class QualityGate:
    """A threshold on how much of a batch may be quarantined."""

    max_quarantine_fraction: float = DEFAULT_MAX_QUARANTINE_FRACTION

    def __post_init__(self) -> None:
        if not 0.0 <= self.max_quarantine_fraction <= 1.0:
            raise ValueError(
                f"max_quarantine_fraction must be between 0 and 1, "
                f"got {self.max_quarantine_fraction}"
            )

    @classmethod
    def from_env(cls, environ: dict[str, str] | None = None) -> QualityGate:
        """Build a gate, letting the environment override the threshold."""
        source = environ if environ is not None else dict(os.environ)
        raw = source.get(THRESHOLD_ENV_VAR)
        if raw is None:
            return cls()
        try:
            return cls(max_quarantine_fraction=float(raw))
        except ValueError as exc:
            raise ValueError(f"{THRESHOLD_ENV_VAR} must be a number, got {raw!r}") from exc

    def assess(self, result: SilverResult) -> GateReport:
        """Measure a silver split against the threshold without acting on it."""
        return GateReport(
            total=result.rows_in,
            quarantined=len(result.quarantine),
            threshold=self.max_quarantine_fraction,
        )

    def enforce(self, result: SilverResult) -> GateReport:
        """Measure a silver split and stop the run if it is over the threshold.

        Returns:
            The report, when the batch is within the threshold.

        Raises:
            QualityGateError: The quarantined share exceeded the threshold.
        """
        report = self.assess(result)
        if not report.passed:
            raise QualityGateError(
                quarantined=report.quarantined,
                total=report.total,
                fraction=report.fraction,
                threshold=report.threshold,
            )
        return report


def _name_counts(column: pd.Series) -> dict[str, int]:
    """Count occurrences of each name in a column of comma-joined rule names."""
    counts: Counter[str] = Counter()
    for value in column.fillna(""):
        counts.update(name for name in (part.strip() for part in value.split(",")) if name)
    return dict(counts.most_common())


def quarantine_reason_counts(quarantine: pd.DataFrame) -> dict[str, int]:
    """Return how many rows each rejecting rule sent to quarantine.

    A row that failed two rules is counted once against each, so the totals here
    can exceed the number of quarantined rows.
    """
    if quarantine.empty:
        return {}
    return _name_counts(quarantine["_failed_rules"])


def warning_counts(clean: pd.DataFrame) -> dict[str, int]:
    """Return how many published rows carry each warning."""
    if clean.empty:
        return {}
    return _name_counts(clean["_warnings"])
