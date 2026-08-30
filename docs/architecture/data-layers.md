# Research Data Layers

The research pipeline uses four one-way layers. This is an incremental
boundary for existing modules, not a requirement to rewrite working code.

```text
provider artifacts -> normalized facts -> derived features -> research decisions
       raw                 fact              feature              decision
```

## Invariants

- **Raw** keeps provider-owned evidence, hashes, fetch time and source version.
  It is append-only and does not contain local investment labels.
- **Fact** normalizes a sourced observation such as OHLCV, shares, a filing or
  a corporate action. Every value has source, as-of date and quality status.
- **Feature** is reproducible from facts and a named calculation version, such
  as RSI, KDJ, revenue growth or cash runway. It never overwrites facts.
- **Decision** is a versioned research conclusion, score or trade state. It
  references its input batch and rule version and is never presented as a fact.

`DataQualityMetadata` is the common API contract. Provider conflicts cross the
raw/fact boundary only through `DataQualityIncident`: the bad entity is
quarantined, unrelated entities continue, and a later successful retry closes
the incident automatically.

## P1 research views

- Longbridge EPS forecasts, institutional/fund holdings, analyst aggregates
  and valuation payloads remain normalized provider facts. Their API views now
  expose `layer`, `source`, `source_version`, `as_of` and `quality_status`.
- The local fair-value range is explicitly a versioned `feature`; it never
  overwrites provider valuation facts or presents itself as intrinsic value.
- Candidate catalyst timelines merge SEC facts and local user expectations but
  preserve `evidence_type`. A user catalyst is a `decision`, and is marked
  incomplete until both a date and source are supplied.

## P2 research decisions

- Candidate watches separately store company-thesis status, security-research
  readiness and the current research action. These concepts must not be
  inferred from one another.
- Every material change appends a `CandidateResearchMemoVersion`, including
  the action threshold, its origin (`inherited`, `draft`, or `approved`) and
  rationale. Existing versions remain immutable.
- The manual research portfolio aggregates decision-layer weight limits with
  fact/feature-layer liquidity, readiness, price and catalyst evidence. Missing
  inputs create risk flags; they never become zero-risk assumptions.

## Benchmark validation

- IWM is a first-class fact series, fetched before candidate history and
  persisted as daily OHLCV. A candidate-history batch cannot hide a missing
  benchmark behind an overall success status.
- Benchmark readiness requires both the configured history depth and coverage
  through the current published market date. Missing, insufficient and stale
  are separate states.
- Candidate-effectiveness maturity is independent from benchmark-history
  readiness. Validation requires at least 30 mature paired observations and at
  least 5 distinct signal dates; many securities entering on one day are not
  treated as independent market-regime evidence.
- Every immutable signal owns durable 1/5/20/60-day outcome rows. Daily market
  refreshes advance them from pending to mature, preserve the first maturity
  timestamp and pair candidate returns with IWM without mutating the signal.
- Effectiveness reports isolate the current scoring version, so results from a
  superseded rule set cannot silently validate the active decision rule.
- Historical replay reads only published score snapshots and the exact price
  snapshot referenced by that batch. Missing completion timestamps, scores
  written after completion and prices after the batch effective date are
  rejected instead of being repaired with today's facts.
- Reports disclose a 0.5% assumed round-trip cost and add median, P25/P75 and
  95% confidence intervals. Market-cap, sector, trailing dollar-liquidity, IWM
  regime and signal-type segments use only facts available on the signal date.

## Daily decision gate

- The dashboard independently reports whether local snapshots are usable for
  research and whether they are sufficient to form a new trade plan.
- Fresh market data, a published candidate batch, decision-critical fact
  coverage, a complete outcome tracker and validated effectiveness are required
  for the green gate. Unverified effectiveness deliberately limits the system
  to research even when every current price is present.
- Entity-level gaps remain isolated: a ticker waiting for technical history is
  excluded from actionability without blocking complete tickers. Expired market
  data, a missing candidate batch or a critical operational issue blocks the
  global gate.
- The same gate is enforced on the research-position write API. New or larger
  allocations are rejected when blocked; reductions and notes-only changes are
  still accepted. A deliberate override requires a meaningful reason and emits
  an immutable audit event.

## Provider and portfolio observability

- Provider health includes last-20-attempt usable and complete rates plus
  trading-day freshness. A partial response with usable records is separated
  from a complete run instead of being counted as an all-or-nothing success.
- Research-position aggregation reports largest-name and top-three weights,
  normalized concentration, reference-return coverage, estimated daily
  liquidity capacity and weights exposed to liquidity, capital, event and data
  gaps. Local daily closes provide IWM beta, annualized volatility, 20-day
  momentum, pair correlations and explicit market/sector/liquidity/event stress
  scenarios; every result carries sample and coverage boundaries.
- Earnings consensus is append-only and frozen by fetch time. A cycle closes
  only when a pre-report snapshot, reported actual and local post-report price
  series can be aligned without look-ahead. Guidance remains `not_covered`
  until an explicit management source is structured.
- Valuation views freeze the provider peer universe from the earliest successful
  local snapshot, retain cross-batch multiple history and calculate own-history
  percentiles. Peer growth, margin and runway appear only when local point-in-time
  fundamentals exist.
- In-app notification creation centrally adds priority, why-now, thesis impact,
  suggested action, review time and the deterministic event-key deduplication
  boundary.

## Historical-price recovery

- Every incomplete ticker owns a durable retry checkpoint with its failure
  reason, sample depth, attempt count and next retry time. Scheduled warmups
  only request due tickers; a manual operator action may explicitly bypass the
  backoff window.
- Provider errors, empty responses, insufficient history and stale history are
  separate conditions with bounded exponential backoff. Five consecutive
  failures move a ticker to the visible `manual_review` queue and remove it
  from automatic scheduling until an operator retries it.
- A failed ticker opens a fact-layer `DataQualityIncident`; a later complete,
  current OHLCV series resolves both the retry checkpoint and incident.
- Health metrics are scoped to the current published candidate batch. A retry
  for one ticker never deletes or re-downloads already complete histories.

## Current mapping

| Layer | Existing durable records |
| --- | --- |
| Raw | source versions, provider runs, SEC cache/artifacts, source checkpoints |
| Fact | securities, listings, price/share/financial/filing/capital-risk snapshots |
| Feature | financial metrics, technical analysis, valuation and market-quality calculations |
| Decision | candidate scores, research readiness, signal events, trade setup states and simulations |

## Migration rule

New work must enter at the lowest truthful layer. Existing modules migrate when
they are touched for a business requirement. Cross-layer writes should be
split into idempotent stages with a durable batch or entity/date checkpoint.
