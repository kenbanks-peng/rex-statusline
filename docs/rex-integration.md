# Rex integration

Requires Rex CLI/API revision 11.

## Layout

The manager uses one shared float per tab, rendering the focused terminal split.
These are window-scoped overlays, **not native terminal decorations**.
`python3 tests/smoke.py` checks the shared-float lifecycle in a disposable session.
Set `SMOKE_SHOW` to a Rex client label to display the test session; optionally set
`SMOKE_WINDOW` to its macOS window number to save screenshots in `build/smoke/`.
The client's previous session is restored afterward.

## Minimum integration

- `session.list`, `session.view`: discover blocks and normalized pane rectangles.
- `session_view_changed`: reconcile moves, splits, zoom, closure, and layering.
- Terminal `size`: scale a row and rendered text width relative to its source.
- `session.new_layer`, `resize_layer`, `set_layer_state`, `close_layer`:
  manage one shared float per tab. Moving a split leaves its source tab’s float in place.
- Zoom retains the unzoomed rectangles. Use full-window geometry for the zoomed
  source. The same tab float displays its status during zoom and after unzoom.
- `focused_block_id` selects the source. Hide the float without terminal context.
  Scale its size from source cells; position it within the tab using configured
  placement or dragged coordinates.
- Exclude status blocks from terminal discovery (labels `rex-status` and
  `rex-statusline:`-prefixed labels).

## Boundaries

Overlays occupy terminal content; they do not reserve a row. Rex exposes cell
sizes and normalized layout rectangles, not exact client content insets. Heights
therefore use the source’s rows, cell height, and normalized pane height to
estimate the viewport. On macOS, the saved `Workspace.terminalPaddingY` preference
adds padding on both vertical edges to both the source estimate and the single
source-font row. It is reread on each reconciliation; zero means no padding.
An absent setting or unsupported platform retains the conservative 8-pixel display
allowance, while read errors retain the last successful setting. Pane headers and
other unreported decorations are not measured. Neither display row counts nor its cell metrics are sizing feedback: Rex clamps
rows to at least one and reports reduced cell metrics when the float is clipped.
The status font must match the source font; independent status-font sizing is not
reliably available through this API. This avoids resize-history-dependent
shrinking but does not promise pixel-perfect native decoration sizing. A window resize updates terminal sizes; periodic
reconciliation supplements events.

The manager derives height and width from source terminal dimensions. Linux
builds and simulated lifecycle tests do not prove Linux-client visual behavior.
