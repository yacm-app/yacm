// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 The YACM Authors. Additional terms under GPLv3 section 7: see NOTICE.md.

// Folders that keep their contents sorted. Your own folders keep the
// choice on the group (sortBy); the top level and team library folders
// keep it on this computer (localSorts, key "" = top level). A sort with
// subfolders also applies to subfolders that have none of their own;
// "manual" stops that for one folder.

/** @typedef {{ key: string, subfolders?: boolean }} FolderSort */

export const SORT_LABELS = {
  name: 'Name (A to Z)',
  'name-desc': 'Name (Z to A)',
  address: 'Address',
  type: 'Connection type',
  recent: 'Opened last',
  count: 'Most sessions (subfolders)',
};

/** ownSort is the sort a folder sets itself (id '' = top level). */
export function ownSort(st, id) {
  if (!id) return st.localSorts?.[''] || null;
  const n = st.byId.get(id);
  if (!n?.isGroup) return null;
  return (n.library ? st.localSorts?.[id] : n.sortBy) || null;
}

/** inheritedSort is the sort folder id gets from a parent (a sort that
 * includes subfolders), ignoring its own: { sort, from } or null. */
export function inheritedSort(st, id) {
  if (!id) return null;
  for (let p = st.byId.get(id)?.parentId || '', i = 0; i < 64; i++) {
    const s = ownSort(st, p);
    if (s) return s.key === 'manual' || !s.subfolders ? null : { sort: s, from: p };
    if (!p) break;
    p = st.byId.get(p)?.parentId || '';
  }
  return null;
}

/** sortOf returns { sort, from } that applies inside folder id: its own,
 * or the nearest parent's that includes subfolders. from is the folder that
 * set it ('' = top level). sort is null for your own order. */
export function sortOf(st, id) {
  const own = ownSort(st, id);
  if (own) return own.key === 'manual' ? { sort: null, from: id } : { sort: own, from: id };
  return inheritedSort(st, id) || { sort: null, from: '' };
}

const natural = new Intl.Collator(undefined, { numeric: true, sensitivity: 'base' });

// ipKey turns an IPv4 address into a sortable number; other hosts sort
// after all addresses, by name.
function ipKey(host) {
  const m = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/.exec(host || '');
  if (!m) return null;
  return m.slice(1).reduce((a, x) => a * 256 + Math.min(255, +x), 0);
}

const TYPE_ORDER = { ssh: 0, telnet: 1, serial: 2 };

/** sortList sorts one folder's children in place by sort. Groups stay
 * before sessions and team libraries at the bottom (rank). */
export function sortList(list, sort, { rank, recent, count }) {
  const byName = (a, b) => natural.compare(a.name, b.name);
  let cmp;
  switch (sort.key) {
    case 'name-desc': cmp = (a, b) => -byName(a, b); break;
    case 'address': cmp = (a, b) => {
      if (a.isGroup || b.isGroup) return byName(a, b);
      const x = ipKey(a.session?.host), y = ipKey(b.session?.host);
      if (x !== null && y !== null) return x - y || byName(a, b);
      if (x !== null) return -1;
      if (y !== null) return 1;
      return natural.compare(a.session?.host || '', b.session?.host || '') || byName(a, b);
    }; break;
    case 'type': cmp = (a, b) => {
      if (a.isGroup || b.isGroup) return byName(a, b);
      const t = p => TYPE_ORDER[p] ?? 9;
      return t(a.session?.protocol) - t(b.session?.protocol) || byName(a, b);
    }; break;
    case 'recent': cmp = (a, b) => {
      const r = n => { const i = recent.indexOf(n.id); return i < 0 ? Infinity : i; };
      const x = r(a), y = r(b);
      return x === y ? byName(a, b) : x - y;
    }; break;
    case 'count': cmp = (a, b) => (a.isGroup && b.isGroup ? count(b.id) - count(a.id) : 0) || byName(a, b); break;
    default: cmp = byName;
  }
  list.sort((a, b) => rank(a) - rank(b) || cmp(a, b));
}

/** applySorts sorts every folder that keeps itself sorted. */
export function applySorts(st, rank) {
  const counts = new Map();
  const count = id => {
    if (counts.has(id)) return counts.get(id);
    let c = 0;
    for (const n of st.children.get(id) || []) c += n.isGroup ? count(n.id) : 1;
    counts.set(id, c);
    return c;
  };
  const recent = st.recent || [];
  for (const [id, list] of st.children) {
    const { sort } = sortOf(st, id);
    if (sort) sortList(list, sort, { rank, recent, count });
  }
}
