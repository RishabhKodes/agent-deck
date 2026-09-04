package ui

// outputWheelScrollLines is deliberately larger than the one-row keyboard
// step used by read-only pagers. Three rows per wheel notch matches the rest of
// the dashboard and keeps trackpad/wheel scrolling responsive.
const outputWheelScrollLines = 3

// selectedPreviewScrollKey returns the cache identity for the pane currently
// rendered in PREVIEW. Local session windows and remote sessions have distinct
// keys, so frozen history can never leak across a selection change.
func (h *Home) selectedPreviewScrollKey() string {
	if _, key, _ := h.selectedPreviewTarget(); key != "" {
		return key
	}
	if _, _, key, ok := h.selectedRemotePreviewTarget(); ok {
		return key
	}
	return ""
}

// resetPreviewScroll returns the preview to its live tail and releases the
// frozen history buffer. Call this on every session/mode transition as well as
// when scrolling reaches the bottom.
func (h *Home) resetPreviewScroll() {
	h.previewScrollOffset = 0
	h.previewScrollSnapshot = ""
	h.previewScrollSnapshotKey = ""
	h.previewScrollSnapshotSet = false
}

// freezePreviewForScroll captures the exact cache value visible when the user
// first leaves the live tail. The live cache continues to refresh behind it;
// rendering this immutable copy prevents incoming agent output from moving the
// text being read.
func (h *Home) freezePreviewForScroll() bool {
	key := h.selectedPreviewScrollKey()
	if key == "" {
		return false
	}
	if h.previewScrollSnapshotSet && h.previewScrollSnapshotKey == key {
		return true
	}

	h.previewCacheMu.RLock()
	content, ok := h.previewCache[key]
	h.previewCacheMu.RUnlock()
	if !ok {
		return false
	}

	h.previewScrollSnapshot = content
	h.previewScrollSnapshotKey = key
	h.previewScrollSnapshotSet = true
	return true
}

// scrollPreviewBy moves relative to the live tail. Positive deltas reveal
// older rows; negative deltas move toward live output.
func (h *Home) scrollPreviewBy(delta int) {
	if delta == 0 {
		return
	}
	if delta > 0 {
		if !h.freezePreviewForScroll() {
			return
		}
		maxInt := int(^uint(0) >> 1)
		if h.previewScrollOffset > maxInt-delta {
			h.previewScrollOffset = maxInt
		} else {
			h.previewScrollOffset += delta
		}
		return
	}

	h.previewScrollOffset += delta
	if h.previewScrollOffset <= 0 {
		h.resetPreviewScroll()
	}
}

// scrollPreviewToOldest uses a sentinel that the renderer clamps against its
// actual content-row budget. Keeping that calculation in rendering avoids
// duplicating the metadata/notes height logic here.
func (h *Home) scrollPreviewToOldest() {
	if !h.freezePreviewForScroll() {
		return
	}
	h.previewScrollOffset = int(^uint(0) >> 1)
}

func (h *Home) previewPageStep() int {
	_, rows, ok := h.focusedAgentTerminalDimensions()
	if !ok {
		rows = h.height / 2
	}
	step := rows - 1 // preserve one row of reading context
	if step < 1 {
		step = 1
	}
	return step
}

// previewContentForScroll selects the frozen buffer only for the pane that
// owns it. A stale key is cleared defensively even if a future navigation path
// forgets to call resetPreviewScroll.
func (h *Home) previewContentForScroll(key, live string) string {
	if h.previewScrollOffset <= 0 {
		return live
	}
	if h.previewScrollSnapshotKey != "" && h.previewScrollSnapshotKey != key {
		h.resetPreviewScroll()
		return live
	}
	if h.previewScrollSnapshotSet {
		return h.previewScrollSnapshot
	}
	return live
}

// clampPreviewScrollOffset bounds the tail-relative offset for the row budget
// chosen by a renderer. If there is no overflow, the pane immediately resumes
// live rendering and drops its frozen snapshot.
func (h *Home) clampPreviewScrollOffset(maxOffset int) int {
	if maxOffset <= 0 {
		h.resetPreviewScroll()
		return 0
	}
	if h.previewScrollOffset < 0 {
		h.previewScrollOffset = 0
	}
	if h.previewScrollOffset > maxOffset {
		h.previewScrollOffset = maxOffset
	}
	return h.previewScrollOffset
}

// mouseInPreview reports whether a wheel event lands inside the visible
// PREVIEW pane. Dual layouts route by X and stacked layouts by Y; narrow
// single-column layouts have no preview target.
func (h *Home) mouseInPreview(x, y int) bool {
	switch h.getLayoutMode() {
	case LayoutModeDual:
		return x >= h.sessionsPaneWidth()
	case LayoutModeStacked:
		top := h.stackedPreviewTopY()
		return top >= 0 && y >= top
	default:
		return false
	}
}
