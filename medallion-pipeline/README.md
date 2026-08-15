# Medallion pipeline with a quality gate (Python)

**Requirement this addresses:** data quality controls with quarantine and reporting of failed records, not silent inclusion.

A bronze / silver / gold pipeline over a batch of taxi trip records. Bronze keeps every line of the
source file exactly as it arrived. Silver types it, deduplicates it, and applies a named rule set
row by row, splitting the batch into clean rows and a **quarantine table** that carries the original
record plus the name of every rule it failed. A configurable gate then decides whether the batch is
fit to publish at all: if too much of it was quarantined, the run fails and no gold table is written.

Pandas and Parquet only. No cluster, no cloud, no network at test time.

## What it demonstrates

- **Quarantine is an output, not a log line.** The fourteen bad records in the sample batch are
  written to `output/quarantine/trips.parquet` with `_failed_rules` and `_quarantined_at` beside the
  untouched original fields. "One row was dropped" is not an answer anyone can act on; "line 46
  failed `fare_amount_non_negative` with a fare of -12.50" is.
- **Rules are data.** `rules.py` is a list of `Rule` objects -- name, severity, description,
  predicate -- readable top to bottom by someone who does not write Python. Adding a rule means
  appending an entry; nothing in the silver transform changes. A test asserts that every rejecting
  rule has a case proving it, so a rule cannot be added without evidence that it fires.
- **Two severities, two behaviors.** `REJECT` quarantines the row. `WARN` publishes it with the
  warning attached, so an implausible 212-mile metered trip gets reviewed rather than deleted by
  someone's judgment call.
- **Precise reasons.** A predicate that cannot apply -- a range check on a field that was never sent
  -- passes, and lets the rule that *does* apply own the failure. One broken field yields one
  reason. A record with two faults yields both.
- **Deduplication with an audit trail.** A re-sent record is recognized by its bronze content hash
  and quarantined as `unique_trip_record` rather than silently discarded, so the sender can be told
  the batch contained a repeat.
- **The gate.** `QualityGate` fails the run with `QualityGateError` when the quarantined share
  exceeds the threshold. A gold table built from the surviving 60% of a broken extract looks exactly
  like a healthy one -- same columns, plausible numbers, no error anywhere -- and it will be briefed
  as if it were complete.
- **Reconciliation.** `gold.reconcile` re-derives the silver trip count and fare total from the
  published aggregate and refuses the run if they disagree. It is what makes the number on the slide
  defensible: gold accounts for every silver row exactly once, and for no row silver rejected.
- **Bronze is lossless.** A test proves a record silver quarantines is still present, verbatim, in
  bronze. The layer that rejects nothing is what lets you answer a question about a rejected row
  three months later.

## Layout

| File | Contents |
| --- | --- |
| `pipeline/bronze.py` | Schema-on-read ingest. Every field as text, plus source file, source line, ingestion time, and a content hash. |
| `pipeline/rules.py` | The rule set: `Rule`, `Severity`, the predicates, and `evaluate`. |
| `pipeline/silver.py` | Typing, duplicate marking, row-wise rule application, and the clean / quarantine split. |
| `pipeline/quality.py` | `QualityGate`, the gate report arithmetic, and the by-rule counts a run summary prints. |
| `pipeline/gold.py` | Trips, miles, and average fare by day and borough, plus reconciliation against silver. |
| `pipeline/errors.py` | Named failures, one per way the pipeline refuses to publish. |
| `run_pipeline.py` | Entry point: runs every layer, writes Parquet, prints the summary below. |
| `data/taxi_trips_sample.csv` | 140 synthetic records, 14 of them deliberately bad. |
| `tests/test_rules.py` | One case per rejecting rule: the offending row is quarantined and names the right rule. |
| `tests/test_layers.py` | Bronze losslessness, a clean row through all three layers, and reconciliation. |
| `tests/test_gate.py` | The gate raises over the threshold and does not under it. |

## Run it

```bash
cd medallion-pipeline
python -m venv .venv && . .venv/bin/activate
pip install -r requirements.txt
python -m pytest -q
python run_pipeline.py
```

The run writes Parquet under `output/` (gitignored) and prints:

```text
source                        data/taxi_trips_sample.csv
bronze rows ingested           140
silver rows clean              126
quarantined                     14  (10.00% of batch, gate threshold 15.00%)

quarantined by rule:
  passenger_count_in_range        3
  fare_amount_non_negative        2
  dropoff_after_pickup            2
  pickup_borough_known            2
  pickup_timestamp_present        1
  dropoff_timestamp_present       1
  pickup_timestamp_valid          1
  dropoff_timestamp_valid         1
  unique_trip_record              1
  fare_amount_present             1

warnings on published rows:
  trip_distance_plausible         2

gold rows published             34  (reconciled: 126 trips, 7019.60 in fares)
 trip_date pickup_borough  trips  total_miles  total_fare  average_fare
2026-03-02          Bronx      3        20.14      127.54         42.51
2026-03-02       Brooklyn      3        19.28      123.49         41.16
2026-03-02            EWR      1         3.32       25.50         25.50
2026-03-02      Manhattan      7        35.91      237.04         33.86
2026-03-02         Queens      2         8.47       55.57         27.78
2026-03-02  Staten Island      2        12.62       78.73         39.36
2026-03-03          Bronx      3        25.27      156.21         52.07
2026-03-03       Brooklyn      2         9.82       66.50         33.25

outputs under output
```

Fourteen quarantined rows, each with the line it came from and the reason it was held back:

```text
 _source_row tpep_pickup_datetime tpep_dropoff_datetime passenger_count pickup_borough fare_amount                                  _failed_rules
           9                        2026-03-02 13:35:45               1      Manhattan       39.01                       pickup_timestamp_present
          18  2026-03-03 21:09:15                                     6       Brooklyn       42.33                      dropoff_timestamp_present
          27  2026-03-32 08:14:00   2026-03-04 18:40:45               1      Manhattan       62.42                         pickup_timestamp_valid
          36  2026-03-05 07:43:00          not recorded               1      Manhattan       43.14                        dropoff_timestamp_valid
          43  2026-03-05 15:20:45   2026-03-05 15:47:45               1      Manhattan       31.49                             unique_trip_record
          46  2026-03-06 15:01:30   2026-03-06 15:35:30               4       Brooklyn      -12.50                       fare_amount_non_negative
          55  2026-03-07 21:26:15   2026-03-07 22:16:15               2         Queens                                        fare_amount_present
          64  2026-03-08 09:40:30   2026-03-08 09:23:30               2      Manhattan       34.50                           dropoff_after_pickup
          73  2026-03-02 17:50:45   2026-03-02 18:16:45               0       Brooklyn       28.36                       passenger_count_in_range
          82  2026-03-03 18:08:00   2026-03-03 18:57:00               9      Manhattan       53.58                       passenger_count_in_range
          91  2026-03-04 13:40:30   2026-03-04 13:57:30                      Manhattan       18.71                       passenger_count_in_range
         100  2026-03-05 18:55:45   2026-03-05 19:58:45               1       Manhatan       67.36                           pickup_borough_known
         109  2026-03-06 18:41:15   2026-03-06 18:57:15               1        Unknown       16.50                           pickup_borough_known
         118  2026-03-07 09:31:00   2026-03-07 09:25:00               1  Staten Island       -4.80 dropoff_after_pickup, fare_amount_non_negative
```

To watch the gate stop a run, tighten it below the 10% the sample batch quarantines:

```bash
MEDALLION_MAX_QUARANTINE_FRACTION=0.05 python run_pipeline.py
```

```text
GATE FAILED: quality gate failed: 14 of 140 rows quarantined (10.00%), threshold 5.00%
gold not published; quarantine written to output/quarantine
```

The process exits 1, `output/gold/` is not written, and the quarantine table is on disk for the
follow-up. Bronze, silver, and quarantine are all written *before* the gate runs, on purpose: when a
batch is too dirty to publish, the evidence for why must survive the failure.

## Notes

**The data is synthetic.** `data/taxi_trips_sample.csv` was generated for this repository. Its
columns are shaped after the published NYC Taxi and Limousine Commission yellow-taxi trip record
dictionary -- `tpep_pickup_datetime`, `tpep_dropoff_datetime`, `passenger_count`, `trip_distance`,
`fare_amount`, `tip_amount`, `total_amount` -- so the schema is recognizable to anyone who has
worked with that public dataset, but no row here describes a real trip. Real TLC extracts carry a
numeric pickup location identifier that joins to a zone lookup; this sample carries the resolved
borough directly, and `pickup_borough_known` stands in for the lookup miss that join would produce.

The fourteen bad records are deliberate, one per failure mode an ingest actually sees: missing
required fields, a timestamp that does not parse, a dropoff before its pickup, a negative fare,
passenger counts of zero and nine, an unrecognized borough, a re-sent duplicate, and one record that
is wrong in two ways at once.

Money is carried as a float for brevity, so per-group rounding can drift by up to half a cent;
reconciliation allows one cent per published group and nothing more. Production work on money would
use a fixed-point type, and the reconciliation would then be exact.

Nothing here downloads anything. The tests read the committed CSV and write to `tmp_path`; the entry
point writes to `output/`, which is gitignored, so a run leaves nothing to commit.

Provenance is the same instinct as [`../event-sourced-aggregate`](../event-sourced-aggregate),
applied to data rather than to a case record: keep what actually arrived, and derive everything else
from it.
