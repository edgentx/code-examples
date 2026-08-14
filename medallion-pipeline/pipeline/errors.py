"""Named failures for the pipeline.

Every way this pipeline can refuse to publish has a name. A caller -- a
scheduler deciding whether to retry, a test asserting the rule that stopped the
run, an operator reading a log -- branches on the exception type rather than on
a message string.
"""

from __future__ import annotations


class PipelineError(Exception):
    """Base class for every failure this pipeline raises deliberately."""


class SourceNotFoundError(PipelineError):
    """The source file named for ingestion does not exist."""


class SchemaMismatchError(PipelineError):
    """The source file is missing a column the pipeline requires.

    Raised at the bronze boundary. Bronze does not judge *values*, but it does
    insist the file is the file it was told to read: a silently renamed column
    would otherwise become a whole batch of nulls in silver.
    """


class QualityGateError(PipelineError):
    """The quarantined share of a batch exceeded the configured threshold.

    Carries the counts so the operator sees the arithmetic that stopped the run
    without having to open the quarantine table first.
    """

    def __init__(self, quarantined: int, total: int, fraction: float, threshold: float) -> None:
        self.quarantined = quarantined
        self.total = total
        self.fraction = fraction
        self.threshold = threshold
        super().__init__(
            f"quality gate failed: {quarantined} of {total} rows quarantined "
            f"({fraction:.2%}), threshold {threshold:.2%}"
        )


class ReconciliationError(PipelineError):
    """A gold aggregate does not add up to the silver rows it was built from.

    This means rows were invented or lost between layers, which makes every
    number in the published table indefensible.
    """
