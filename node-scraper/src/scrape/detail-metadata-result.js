import { looksLikeStreamUrl } from './detail-streams.js';
import { toStringArray } from './detail-metadata-values.js';

const PROVIDER = 'artlist';

export function mergeMetadata(sources) {
  const merged = {};
  for (const source of sources) {
    if (!source || typeof source !== 'object') continue;
    for (const [key, value] of Object.entries(source)) {
      if (value == null) continue;
      if (Array.isArray(value)) {
        if (value.length > 0) merged[key] = value;
      } else if (typeof value === 'string') {
        if (value !== '') merged[key] = value;
      } else {
        merged[key] = value;
      }
    }
  }
  return merged;
}

export function buildResult({ clipPageUrl, clipId, title, streams, videoSrc, metadata }) {
  const preferredStream = streams.find((u) => looksLikeStreamUrl(u)) || '';
  const preferredVideoSrc = looksLikeStreamUrl(videoSrc) ? videoSrc : '';
  const preferredPreview = looksLikeStreamUrl(metadata.preview_url) ? metadata.preview_url : '';
  const preferredPrimary = looksLikeStreamUrl(metadata.primary_url) ? metadata.primary_url : '';
  const primaryUrl = preferredStream || preferredVideoSrc || preferredPreview || preferredPrimary || clipPageUrl;

  return {
    ok: true,
    provider: PROVIDER,
    clip_id: clipId,
    title: title || metadata.title || clipPageUrl,
    description: metadata.description || '',
    creator: metadata.creator || '',
    country: metadata.country || '',
    location: metadata.location || metadata.country || '',
    tags: toStringArray(metadata.tags),
    categories: toStringArray(metadata.categories),
    page_url: clipPageUrl,
    clip_page_url: clipPageUrl,
    thumbnail_url: metadata.thumbnail_url || '',
    preview_url: preferredPreview || preferredPrimary || preferredStream || preferredVideoSrc || primaryUrl,
    primary_url: primaryUrl,
    stream_urls: streams,
    raw_metadata: metadata.raw_metadata || {},
  };
}
