# IPO Company Sorting Design

## Goal

Make the IPO company view surface the companies with the newest SEC activity first, while allowing users to sort explicitly by IPO status.

## Behavior

- Default order is latest SEC accepted time descending.
- If a company has no SEC accepted time, use its latest filing date.
- Clicking the status column requests server-side status sorting; repeated clicks switch ascending and descending.
- Status order is `new`, `updating`, `effective`, `priced`, `listed`, `withdrawn`, `stale`.
- Within the same status, order by latest SEC activity descending, then company name and CIK ascending for deterministic results.
- Sorting occurs before pagination.

## API

`GET /api/ipo-companies` accepts:

- `sort_by`: `latest_update` (default) or `status`
- `sort_order`: `asc` or `desc`; default is `desc` for `latest_update`

Unknown values fall back to the default latest-update ordering.

## Testing

- Table-driven service tests cover default latest-update order, accepted-time preference, fallback behavior, status ascending/descending, and deterministic ties.
- Existing API tests and the Vue production build must remain green.
