# prom-live-svg

Ultra-high-performance Go service that compiles Prometheus metrics into static SVG charts every 15 seconds. Designed for CDN caching with very low client-side JavaScript overhead.

<p align="center" style="margin: 2em;">
  <img width="400" height="400" style="border-radius: 3%; max-width: 100%" alt="Logo of OuroborosDB" src=".media/Dürer_Melancholia_I.jpg">
</p>

## Usage

`prom-live-svg` will query Prometheus every 15 seconds, compile the configured metrics into SVG and json charts, and serve them on an HTTP endpoint. The browser will load the svg chart on start and then every 15 seconds the new data will be loaded and will be added to the svg chart adding 1 data point every 1 seconds. The browser will always request 15 seconds absolutes like Unix Time: `1779890900`, `1779890915`, `1779890930`, this simplyfies caching and makes it possible to use a CDN to cache the charts and json.


### Quick start

```bash
go run ./cmd/prom-live-svg -check-config -config configs/example.yaml
go run ./cmd/prom-live-svg -config configs/example.yaml
```

### Chart query definitions

Chart queries can be defined directly in YAML with a multiline block:

```yaml
charts:
  - name: chrony_packets_accepted
    query: |-
      rate(chrony_serverstats_ntp_packets_received_total[5m])
      -
      rate(chrony_serverstats_ntp_packets_dropped_total[5m])
```

Or loaded from a separate file relative to the config file location:

```yaml
charts:
  - name: chrony_packets_accepted
    query_file: queries/chrony_packets_accepted.promql
```

Use exactly one of `query` or `query_file` per chart.

Charts can also define headline `stats` queries. Each stat runs its own Prometheus range query, takes the latest sample (summing across returned series), and renders that value prominently in the SVG while the main chart query stays as the background history graph.

If a stat should survive Prometheus/exporter counter resets and app restarts, set `persist: true`. In that mode `prom-live-svg` stores the last observed raw counter and accumulated total in `storage.data_dir/request_stats.json`.

You can also provide `seed_query` / `seed_query_file` to initialize the persistent total from a large historical Prometheus query (for example `increase(...[365d])`). After that, `prom-live-svg` stores the last observed timestamp and only queries Prometheus for the smaller follow-up window from that timestamp to the current request time.

`persist: true` is meant for raw monotonic counter totals like `requests_total`, not rate queries like `rate(...[5m])`. The optional `seed_query` is where a long-range `increase(...[365d])` belongs.

```yaml
charts:
  - name: chrony_requests_summary
    title: Chrony requests
    query_file: queries/chrony_packets_accepted.promql
    lookback: 5m
    step: 15s
    stats:
      - name: all_time_requests
        label: All time requests
        query_file: queries/chrony_packets_total.promql
        seed_query_file: queries/chrony_packets_total_1y.promql
        lookback: 15s
        step: 15s
        decimals: 0
        persist: true
      - name: requests_per_second
        label: Req/s
        query_file: queries/chrony_packets_accepted.promql
        lookback: 15s
        step: 15s
        decimals: 0
        unit: req/s
```

### Chart HTTP endpoints

The HTTP server now exposes chart endpoints in two formats:

Timestamped endpoints:

- `/charts/{chart}/{unix_timestamp}.json`
- `/charts/{chart}/{unix_timestamp}.svg`

Shorthand latest endpoints:

- `/charts/{chart}.json`
- `/charts/{chart}.svg`

Live HTML view endpoints:

- `/live/{chart}`
- `/live/{chart}/{unix_timestamp}`

Examples:

```text
/charts/chrony_packets_accepted/1779896355.json
/charts/chrony_packets_accepted/1779896355.svg
/charts/chrony_packets_accepted.json
/charts/chrony_packets_accepted.svg
/live/chrony_packets_accepted
/live/chrony_packets_accepted/1779896355
```

Behavior:

- timestamps must be aligned to the configured generation interval, e.g. `15s`
- if a timestamped request targets the next valid quarter-minute and it is still slightly in the future, the server holds the connection until that timestamp is reached
- on process shutdown, in-flight held requests are canceled via the server base context so `Ctrl+C` can stop the service promptly; if graceful shutdown still times out, the server force-closes active connections
- if the requested timestamp is more than one generation interval in the future, the request is rejected
- shorthand endpoints serve the latest aligned chart for the current time
- live HTML views keep the raw timestamped `.svg` and `.json` endpoints cacheable, but use a small amount of browser-side JavaScript to reveal one interpolated point per second from the already-fetched 15-second snapshots
- headline stats such as all-time totals and req/s run in an explicit illusion mode at 25 FPS: req/s uses smoothed multi-snapshot tweening, while counter-like totals use synthetic continuously ticking rates with a small correction back toward real snapshot values
- live HTML views bootstrap with the last three aligned snapshots so the graph and the headline stats animate immediately from first paint and have an extra buffered interval before the next network handoff
- the live view intentionally stays about two generation intervals behind the freshest fetched snapshot so it can keep animating smoothly even when the next timestamped snapshot arrives a little late
- append `?debug=1` to a `/live/...` URL to show an on-page live debug panel and emit console logs for tick progression, fetch timing, buffered seconds, loaded document timestamps, and interpolated stat values while debugging stalls

### Environment overrides

The config loader supports these environment variables:

- `PROM_LIVE_SVG_SERVICE_NAME`
- `PROM_LIVE_SVG_SERVICE_ENVIRONMENT`
- `PROM_LIVE_SVG_HTTP_LISTEN_ADDR`
- `PROM_LIVE_SVG_HTTP_READ_HEADER_TIMEOUT`
- `PROM_LIVE_SVG_HTTP_READ_TIMEOUT`
- `PROM_LIVE_SVG_HTTP_WRITE_TIMEOUT`
- `PROM_LIVE_SVG_HTTP_IDLE_TIMEOUT`
- `PROM_LIVE_SVG_HTTP_SHUTDOWN_TIMEOUT`
- `PROM_LIVE_SVG_PROMETHEUS_BASE_URL`
- `PROM_LIVE_SVG_PROMETHEUS_QUERY_TIMEOUT`
- `PROM_LIVE_SVG_GENERATION_INTERVAL`
- `PROM_LIVE_SVG_CACHE_RETENTION`
- `PROM_LIVE_SVG_STORAGE_DATA_DIR`
- `PROM_LIVE_SVG_LOG_LEVEL`
- `PROM_LIVE_SVG_LOG_FORMAT`

## TODO

- [x] Golang base structure and configuration system
- [x] test data and configurations
- [x] http server basic setup
  - [x] http must hold a connection if it is a valid 1/4 of a minute until the svg or json is available, then it should serve the svg and json charts in said request, so we can handle browsers that have a fast running clock.
- [x] SVG renderer with SVG template
- [ ] caching setup for svg and json charts
  - [ ] Multi read and single write lock with fallback for the reads to the previous svg and json charts.
  - [ ] Must cache svg and json for 15 min
- [ ] Provide prometheus exporter for the service itself so we can monitor requests, query time and draw times.


### Annotations

#### Annotation for generated tests:

If a test file is generated by AI, it must include `_A` before `_test.go` in the filename, e.g., `example_A_test.go`. This indicates that the tests were generated by AI.  
Humans should not add tests in files with the `_A` suffix, and should instead create new test files without the `_A` suffix to indicate that these tests were written by humans.

#### Annotation legend for function comments:
To indicate the correctness and safety of the logic of functions, the following annotations are used in comments directly after the function definitions, at the same line as func (See examples below):

- `// A` - Function and was written by **AI** and was not reviewed by a **human**.
- `// AP` - Function was written by **AI** and was reviewed but the **human** has found a potential issue which the **human** marked with a `// TODO ` comment.
- `// AC` - Function was written by **AI** and was reviewed and approved by a **human** that has medium confidence in the correctness and safety of the logic.
- `// H` - Function was written by a **human**
- `// HP` - Function was written by a **human** but the **human** has found a potential issue which the **human** marked with a `// TODO ` comment.
- `// HC` - A **human** comprehended the logic of th function in all its dimensions and is confident about its correctness and safety.

If the function has a higher risk profile (e.g., involves complex algorithms, security-sensitive operations, or critical data handling), a `P` prefix is added for `Priority`:

**All `P` function must be brought to `PHC` status before a production release.**

We add the indicators directly after the function declaration, although it is normally not common practice in Go, because it makes it easier to see the status of the function for most editors as they show use sticky function declaration.

It is negotiable that AI generated functions must be generated with an `// A` or `// AP` annotation after the function declaration `func exampleFunction() { // A`.

Examples:  
```go

// This function does X, Y, and Z.
func exampleFunction() { // A
    // Function is low risk and was written by AI and not reviewed by a human.
}

// This function does X, Y, and Z.
func exampleFunction() { // HC
    // Function is low risk and was comprehended by a human who is confident about its correctness and safety.
}

// This function performs critical operations X, Y, and Z has some funky stuff going on.
func criticalFunction() { // PAP
    // Function is high risk and was comprehended by a human who is confident about its correctness and safety.
}

// This function performs critical operations X, Y, and Z.
func criticalFunction() { // PHC
    // Function is high risk and was comprehended by a human who is confident about its correctness and safety.
}

// If a function has multiline parameters, the annotation goes at the same line as func
func manyParametersFunction( // AC
    param1 string, 
    param2 int, 
    param3 []byte
) error { 
    // function has many parameters, was written by AI and reviewed and approved by a human.
    return nil
}
```

## Logo 

The logo is [Albrecht Dürer's Melancholia I (1514)](https://commons.wikimedia.org/wiki/File:D%C3%BCrer_Melancholia_I.jpg)

## License
ouroboros-db © 2026 Mia Heidenstedt and contributors   
SPDX-License-Identifier: AGPL-3.0
