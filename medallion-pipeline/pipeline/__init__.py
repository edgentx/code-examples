"""A bronze / silver / gold pipeline with a data-quality gate.

Layer boundaries, in one place:

* :mod:`pipeline.bronze` ingests verbatim and rejects nothing.
* :mod:`pipeline.silver` types, deduplicates, and validates against
  :mod:`pipeline.rules`, producing clean rows and a quarantine table.
* :mod:`pipeline.quality` decides whether the batch is fit to publish at all.
* :mod:`pipeline.gold` aggregates clean silver rows and reconciles against them.
"""

from __future__ import annotations

from .errors import (
    PipelineError,
    QualityGateError,
    ReconciliationError,
    SchemaMismatchError,
    SourceNotFoundError,
)

__all__ = [
    "PipelineError",
    "QualityGateError",
    "ReconciliationError",
    "SchemaMismatchError",
    "SourceNotFoundError",
]
