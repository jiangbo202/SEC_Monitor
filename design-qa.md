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
