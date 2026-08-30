# Design QA — 全项目紧凑表单控件

## Evidence

- Source visual truth: `/var/folders/pv/6vtxntld1znbklrq3_9tnnpc0000gn/T/TemporaryItems/NSIRD_screencaptureui_dRPodd/截屏2026-08-23 06.56.27.png`
- Source pixels: 2398 × 696 (macOS high-density capture); normalized comparison copy: `/private/tmp/compact-controls-source.png`, 1106 × 321.
- Implementation URL: `http://127.0.0.1:9090/`
- Narrow implementation captures (CSS viewport 537 × 580, reported DPR 2; screenshot pixels 537 × 580):
  - `/private/tmp/compact-controls-filings.png`
  - `/private/tmp/compact-controls-ipo.png`
  - `/private/tmp/compact-controls-sync-runs.png`
  - `/private/tmp/compact-controls-configs.png`
- Desktop implementation capture: `/private/tmp/compact-controls-desktop-sync-runs.png`, 1280 × 800 CSS/screenshot pixels.
- Combined source/implementation comparison: `/private/tmp/compact-controls-source-and-final.png`, 1106 × 1513.
- State: representative input and select filters visible; first enabled select expanded on each captured page.

## Findings

- No actionable P0/P1/P2 findings remain.
- Fonts and typography: text-like controls and dropdown options consistently use the existing UI family at 12px; long task labels truncate instead of widening their menu.
- Spacing and layout rhythm: input wrappers, select triggers, date controls and input-number controls use a 28px compact height; dropdown rows use 28px line boxes with 10px horizontal padding. Multiline text areas retain their functional height with compact inner padding.
- Colors and tokens: existing Element Plus focus, border, disabled and semantic tokens are unchanged.
- Image quality/assets: no image assets are introduced or modified by this change.
- Copy/content: labels, option values and application terminology are unchanged. Truncation is limited to overlong menu values.
- Accessibility and behavior: focus rings remain visible; controls remain keyboard-capable; menu rows remain readable and usable. No page-level horizontal overflow was found at either tested viewport.
- Full-view evidence: 24 routes were checked at the narrow and 1280px desktop viewports. Eighteen routes expose visible text/select controls; all passed automated dimensional checks.
- Focused comparison evidence: the combined image shows the compact small-cap rhythm against SEC 公告, IPO 监控, 任务执行历史 and 配置中心 with their dropdowns open. Additional focused crops were unnecessary because labels and option rows are legible in the representative captures.

## Interaction QA

- Opened the first enabled select on every route that exposes one, measured trigger/menu/option dimensions, and closed it with Escape.
- Verified every menu width matches its trigger within 1px, including the long task-name menu.
- Verified inputs and selects are 28px high with 12px text; dropdown items are 28px high with 12px text and `0 10px` padding.
- Browser console errors checked: 0.
- Document horizontal-overflow violations: 0.

## Comparison History

1. Initial P2: most pages used 32px inputs/selects, 13–14px text and 34px dropdown rows, unlike the compact small-cap reference.
   - Fix: introduced global, text-control-specific compact sizing and global teleported-popper rules.
2. Iteration P2: input wrappers measured 30px while select triggers measured 28px because padding was added outside the declared height.
   - Fix: applied `box-sizing: border-box` to compact wrappers.
3. Iteration P2: long option values could expand a dropdown from a 248px trigger to 578px.
   - Fix: enabled `fit-input-width` on all 51 `el-select` instances; long text now truncates within the trigger width.
4. Final evidence: all 24 routes pass at narrow and desktop viewports with zero dimensional, width, overflow or console-error violations.

## Follow-up Polish

- None required for this scoped consistency pass.

final result: passed

---

# Design QA — 内幕交易来源筛选

- Source visual: `/var/folders/pv/6vtxntld1znbklrq3_9tnnpc0000gn/T/TemporaryItems/NSIRD_screencaptureui_d3vNik/截屏2026-08-30 02.55.06.png`
- Implementation evidence: in-app browser capture of `http://127.0.0.1:9090/insider-trading`
- Source pixels: 2462 × 1198
- Implementation viewport: 1280 × 720 CSS pixels
- Tested state: `10b5-1 计划` tab; 来源分别为 `小盘候选`、`监控标的` 和重置后的 `全部来源`

## Full-page comparison

- Typography, spacing, colors, borders and density remain consistent with the existing compact Insider Trading page.
- The new source control sits in the existing filter row without changing the page hierarchy or forcing an additional row.
- Existing cards, status notice, actions and table layout remain unchanged.

## Focused comparison: source selector

- Added a compact `来源` selector between Ticker and the existing status/direction filters.
- Options are `监控标的` and `小盘候选`; the clear/default state is `全部来源`.
- The control has a fixed 128 px width so all three labels remain readable without creating excessive whitespace.
- The same control and behavior are present on both `交易记录` and `10b5-1 计划` tabs.

## Interaction and data checks

- Transaction source `监控标的`: query succeeded, `Total 768`.
- Plan source `小盘候选`: query succeeded, `Total 114`.
- Reset restored `全部来源` and the unfiltered plan total, `Total 131`.
- Direct Docker API checks returned the same scoped totals.
- Docker service is healthy; recent request logs contain successful 200 responses and no runtime error for the tested flow.

## Iteration history

1. First implementation used content-fitting width. Browser QA found the selected value collapsed to an arrow-only control (P1 usability issue).
2. Removed content-fitting behavior and set the selector to 128 px. Rebuilt the Docker image and repeated both source filters and reset flow.
3. Final comparison found no remaining actionable P0, P1 or P2 issues in the changed area.

## Final result

passed

---

# Design QA — 内幕交易筛选宽度与计划状态文案

- Source visuals:
  - `/Users/jiangbo/Downloads/截屏2026-08-30 03.09.49.png`
  - `/Users/jiangbo/Downloads/截屏2026-08-30 03.10.15.png`
- Implementation evidence: in-app browser captures of `http://127.0.0.1:9090/insider-trading`
- Source pixels: 2014 × 802 and 2318 × 1010
- Implementation viewport/pixels: 1280 × 720 CSS/screenshot pixels, density 1
- Tested states: transaction filters at defaults; plan status set to `已登记`; plan status options opened and checked.

## Findings and comparison history

1. Initial P1: direction, evidence, 10b5-1 and plan-status selects collapsed to arrow-only controls because content-fitting width did not retain a readable empty state.
   - Fix: replaced content-fitting widths with explicit 120–136 px component widths and added descriptive default placeholders.
2. Iteration P2: after widening controls, the transaction action buttons wrapped to a second row at the 1280 px verification viewport.
   - Fix: reduced only the horizontal spacing between this toolbar's form items from the Element Plus default to 8 px; control widths and text readability were preserved.
3. Final pass: all filters and actions fit on one row at 1280 px; selected and default labels are fully readable. The plan tab remains on one row with its wider source/status fields and scan action.

## Required fidelity surfaces

- Fonts and typography: unchanged application font family, size, weight and line height; no new truncation.
- Spacing and layout rhythm: compact single-row toolbar preserved at the verification viewport; alignment and control heights match the existing page.
- Colors and tokens: existing Element Plus tokens and semantic status colors are unchanged.
- Image quality: no raster or vector assets were introduced or modified.
- Copy and content: `有效` is now `已登记`; `执行中` is now `已有执行`. Tooltip copy explains the evidence-based distinction. Default filter labels now identify each unfiltered state.

## Interaction QA

- Opened the plan-status selector and verified both `已登记` and `已有执行` options.
- Selected `已登记` and verified the selected value is visible without clipping.
- Selected transaction direction, evidence and 10b5-1 values and verified each is visible without clipping.
- Switched between both tabs after the Docker rebuild; tables and controls rendered normally.
- Docker service remained healthy and the production frontend build passed.

## Final result

passed
