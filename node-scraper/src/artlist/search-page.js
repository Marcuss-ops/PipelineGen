// src/artlist/search-page.js — thin facade kept for back-compat.
//
// Historically this module exposed a `searchArtlist` that DUPLICATED
// the implementation in artlist_search.js (legacy August 2026 code
// drift). After the canonical modularization the implementation lives
// in artlist/search.js, so this file is now a re-export-only facade.
//
// Any in-tree consumer that still imports from
// `src/artlist/search-page.js` keeps working without changes.

export {
  searchArtlist,
  searchArtlistPreview,
} from '../artlist/search.js';
