"""Run the whole pipeline over the committed sample and print a run summary.

    python run_pipeline.py [source-csv] [output-directory]

Bronze, silver, and quarantine are written before the gate is enforced. That
order matters: when a batch is too dirty to publish, the evidence for why is on
disk already, and the run fails without a gold table having been touched.
"""

from __future__ import annotations

import sys
from pathlib import Path

import pandas as pd

from pipeline import bronze, gold, silver
from pipeline.errors import PipelineError, QualityGateError
from pipeline.quality import QualityGate, quarantine_reason_counts, warning_counts

DEFAULT_SOURCE = Path(__file__).parent / "data" / "taxi_trips_sample.csv"
DEFAULT_OUTPUT = Path(__file__).parent / "output"


def write_table(frame: pd.DataFrame, directory: Path, name: str) -> Path:
    """Write one layer to Parquet and return where it landed."""
    directory.mkdir(parents=True, exist_ok=True)
    path = directory / f"{name}.parquet"
    frame.to_parquet(path, index=False)
    return path


def _display(path: Path) -> str:
    """Show a path relative to the working directory when it sits below it."""
    try:
        return str(path.relative_to(Path.cwd()))
    except ValueError:
        return str(path)


def _print_counts(title: str, counts: dict[str, int]) -> None:
    print(f"\n{title}")
    if not counts:
        print("  (none)")
        return
    for name, count in counts.items():
        print(f"  {name:<28} {count:>4}")


def run(source: Path, output: Path, gate: QualityGate) -> int:
    """Execute the pipeline end to end, printing a summary.

    Returns:
        A process exit code: 0 when gold was published, 1 when the gate stopped
        the run.
    """
    raw = bronze.ingest_csv(source)
    write_table(raw, output / "bronze", "trips")

    result = silver.refine(raw)
    write_table(result.clean, output / "silver", "trips")
    write_table(result.quarantine, output / "quarantine", "trips")

    report = gate.assess(result)
    print(f"source                        {_display(source)}")
    print(f"bronze rows ingested          {report.total:>4}")
    print(f"silver rows clean             {report.published:>4}")
    print(f"quarantined                   {report.quarantined:>4}"
          f"  ({report.fraction:.2%} of batch, gate threshold {report.threshold:.2%})")
    _print_counts("quarantined by rule:", quarantine_reason_counts(result.quarantine))
    _print_counts("warnings on published rows:", warning_counts(result.clean))

    try:
        gate.enforce(result)
    except QualityGateError as failure:
        print(f"\nGATE FAILED: {failure}")
        print(f"gold not published; quarantine written to {_display(output / 'quarantine')}")
        return 1

    published = gold.publish(result.clean)
    gold.reconcile(result.clean, published)
    write_table(published, output / "gold", "trips_by_day_and_borough")
    print(f"\ngold rows published           {len(published):>4}"
          f"  (reconciled: {int(published['trips'].sum())} trips, "
          f"{published['total_fare'].sum():.2f} in fares)")
    print(published.head(8).to_string(index=False))
    print(f"\noutputs under {_display(output)}")
    return 0


def main(argv: list[str] | None = None) -> int:
    """Entry point. Arguments: optional source CSV and output directory."""
    args = list(sys.argv[1:] if argv is None else argv)
    source = Path(args[0]) if args else DEFAULT_SOURCE
    output = Path(args[1]) if len(args) > 1 else DEFAULT_OUTPUT
    try:
        return run(source, output, QualityGate.from_env())
    except PipelineError as failure:
        print(f"{type(failure).__name__}: {failure}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
