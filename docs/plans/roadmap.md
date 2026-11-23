# Roadmap

Standing backlog. Items here are not scheduled; promote one to a plan under `docs/plans/` when it is picked up.

## Benchmark candidates

- Gallery list index: measure the gallery listing query on a large library, then add a supporting index only if the benchmark shows it helps (audit 2 report §3, AUD2-012).
- Keyset pagination for the gallery: replace offset paging with keyset paging if deep pages benchmark slowly (audit 2 report §3, AUD2-012).
- Thumbnail revision lookup: benchmark the per-request revision read plus `Cache-Control: no-store` on gallery thumbnails at a large library (audit 3 report AUD3-025). `no-store` stays until a benchmark justifies a change, because a private cache must not keep thumbnails after logout.
- Cleanup query indexes: benchmark the per-minute cleanup deletes on `WebhookEvent` and `UploadAttempt`, and promote the two indexes when the library passes roughly 100k images (audit 3 report AUD3-026).

## Backlog

- Worker healthcheck and statement timeout (audit 3 report AUD3-023): revisit with a restart policy. A global `statement_timeout` would also cut migrations at startup, and Docker does not restart unhealthy containers today.
