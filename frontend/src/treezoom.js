// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 The YACM Authors. Additional terms under GPLv3 section 7: see NOTICE.md.

// The size of the session list on the left (text, icons, rows): Ctrl/Cmd +
// mouse wheel over it, or Ctrl/Cmd + / - / 0 while it has the focus, and
// Settings > Appearance. The terminal keeps its own font size.
import { state, saveSettings } from './api.js';

export const TREE_MIN = 80;
export const TREE_MAX = 160;

/** @param {number} [pct] */
export function treeSize(pct = state.settings.treeSize) {
  const n = Math.round(Number(pct) || 100);
  return Math.min(TREE_MAX, Math.max(TREE_MIN, n));
}

// applyTreeZoom sets the size from the settings.
export function applyTreeZoom() {
  document.getElementById('sidebar')?.style.setProperty('--tree-zoom', String(treeSize() / 100));
  window.dispatchEvent(new Event('treezoom'));
}

let saveTimer = 0;

// zoomTree changes the size by step (+1 bigger, -1 smaller, 0 back to 100 %).
/** @param {number} step */
export function zoomTree(step) {
  const next = step === 0 ? 100 : treeSize(treeSize() + step * 10);
  if (next === treeSize()) return;
  state.settings = { ...state.settings, treeSize: next === 100 ? 0 : next };
  applyTreeZoom();
  clearTimeout(saveTimer);
  saveTimer = window.setTimeout(() => saveSettings({ treeSize: state.settings.treeSize }).catch(() => {}), 600);
}

// inSidebar: the keyboard focus is in the session list or its search field.
export function inSidebar() {
  return !!document.activeElement?.closest?.('#sidebar');
}

export function initTreeZoom() {
  applyTreeZoom();
  document.getElementById('sidebar')?.addEventListener('wheel', e => {
    if (!(e.ctrlKey || e.metaKey) || e.deltaY === 0) return;
    e.preventDefault();
    zoomTree(e.deltaY < 0 ? 1 : -1);
  }, { passive: false });
}
