import { categories, searchTerms, videoLinks, closeDB } from '../db.js';

/**
 * Saves search results to the database.
 * @param {string} term 
 * @param {object[]} clips 
 * @returns {number} Number of clips saved.
 */
export function saveResultsToDB(term, clips) {
  categories.add('Artlist', 'Imported Artlist search results');
  searchTerms.addMultiple('Artlist', [term]);
  
  const urls = [];
  const meta = [];
  
  for (const clip of clips) {
    if (!clip.primary_url) continue;
    urls.push(clip.primary_url);
    meta.push({
      video_id: clip.clip_id || clip.primary_url,
      file_name: clip.title || clip.clip_id || '',
      width: 0,
      height: 0,
      duration: 0,
    });
  }
  
  const saved = urls.length > 0 ? videoLinks.addMultipleWithSource('Artlist', term, urls, 'artlist', meta) : 0;
  closeDB();
  return saved;
}
