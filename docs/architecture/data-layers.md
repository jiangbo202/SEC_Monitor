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
