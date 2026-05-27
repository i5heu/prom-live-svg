# chrony Prometheus test fixtures

This directory contains real Prometheus API `query_range` responses captured from a Chrony exporter dataset and then sanitized for public unit-test use.

Capture characteristics:

- original capture window: `1779895455` to `1779896355`
- step: `15s`
- result type: Prometheus `matrix`

Fixture files:

- `chrony_packets_accepted.range.json`
- `chrony_timestamp_span.range.json`
- `chrony_source_offset_by_source.range.json`

Query files used to select the matching fixture are stored in:

- `queries/`
- `fixtures.yaml`

## Public-data review

These fixtures were reviewed for repository safety and sanitized before commit:

- removed the real Prometheus base URL
- replaced the real exporter `instance` label with `chrony.example:9123`
- replaced real upstream `source_name` values with generic placeholders like `source-01.example`

The remaining data is metric structure plus numeric time-series values. No personal data is present in these fixtures.

These files are intended for unit tests where a mock Prometheus client returns a fixture response based on the requested query.
