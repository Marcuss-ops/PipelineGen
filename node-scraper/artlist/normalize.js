const CLIP_ID_KEYS = ['id', 'clipId', '_id', 'assetId'];
const TITLE_KEYS = ['title', 'name', 'caption'];
const PAGE_URL_KEYS = ['clipPageUrl', 'clip_page_url', 'pageUrl', 'permalink', 'url', 'link'];

// Known organization/brand names that should not be used as clip titles.
const ORG_TITLE_BLOCKLIST = /^(artlist|artgrid|video|stock footage|clip|download|license|free|search|results| browse|pricing|login|sign up|log in)$/i;
const THUMBNAIL_KEYS = ['thumbnailUrl', 'thumbnail_url', 'coverUrl', 'image'];
const PREVIEW_KEYS = ['previewUrl', 'preview_url', 'videoUrl', 'video', 'src'];
const PRIMARY_KEYS = ['primary_url', 'primaryUrl', 'streamUrl', 'downloadUrl'];

function firstString(value, keys) {
  if (!value || typeof value !== 'object') {
    return '';
  }

  for (const key of keys) {
    const candidate = value[key];
    if (typeof candidate === 'string' && candidate.trim()) {
      return candidate.trim();
    }
    if ((typeof candidate === 'number' || typeof candidate === 'boolean') && String(candidate).trim()) {
      return String(candidate).trim();
    }
  }

  return '';
}

function toNumber(value) {
  const num = Number(value);
  return Number.isFinite(num) ? num : 0;
}

function extractClipIdFromUrl(url) {
  // Match /clip/<slug>/<id> (legacy format) or /clip/<id> (direct format)
  const match = String(url || '').match(/\/clip\/(?:[^/]+\/)?(\d+)(?:[/?#]|$)/);
  return match ? match[1] : '';
}

export function normalizeStrings(value) {
  if (!Array.isArray(value)) {
    return [];
  }

  return value
    .map((item) => {
      if (typeof item === 'string') {
        return item.trim();
      }
      if (item && typeof item === 'object') {
        return String(item.name || item.title || '').trim();
      }
      return '';
    })
    .filter(Boolean);
}

export function normalizeArtlistClip(item, fallbackPageUrl = '') {
  const clip = item && typeof item === 'object' ? item : {};
  const clipPageUrl = firstString(clip, PAGE_URL_KEYS) || String(fallbackPageUrl || '').trim();
  const clipId = firstString(clip, CLIP_ID_KEYS) || extractClipIdFromUrl(clipPageUrl);
  let title = firstString(clip, TITLE_KEYS);

  // Filter out organization/brand names masquerading as clip titles.
  // These come from JSON-LD Organization entities or page-wide elements
  // that leak into the clip data.
  if (title && ORG_TITLE_BLOCKLIST.test(title)) {
    title = '';
  }
  const thumbnailUrl = firstString(clip, THUMBNAIL_KEYS);
  const previewUrl = firstString(clip, PREVIEW_KEYS);
  const primaryUrl = firstString(clip, PRIMARY_KEYS) || previewUrl || clipPageUrl;
  const rawMetadata = clip.raw_metadata && typeof clip.raw_metadata === 'object' ? clip.raw_metadata : clip.rawMetadata;
  const raw = clip.raw && typeof clip.raw === 'object' ? clip.raw : clip;

  return {
    provider: 'artlist',
    clip_id: clipId,
    id: clipId,
    title: title || clipId,
    name: title || clipId,
    description: String(clip.description || ''),
    creator: String(clip.creator?.name || clip.creator || clip.author || clip.artist || clip.contributor || ''),
    country: String(clip.country || clip.countryName || ''),
    location: String(clip.location || clip.shootingLocation || clip.place || ''),
    tags: normalizeStrings(clip.tags || clip.keywords),
    categories: normalizeStrings(clip.categories),
    page_url: clipPageUrl,
    clip_page_url: clipPageUrl,
    thumbnail_url: thumbnailUrl,
    preview_url: previewUrl || primaryUrl,
    primary_url: primaryUrl,
    stream_urls: Array.isArray(clip.stream_urls) && clip.stream_urls.length > 0
      ? clip.stream_urls.map((url) => String(url)).filter(Boolean)
      : (primaryUrl ? [primaryUrl] : []),
    duration_seconds: toNumber(clip.duration_seconds || clip.durationSeconds || clip.duration || 0),
    duration_ms: toNumber(clip.duration_ms || clip.durationMs || 0),
    width: toNumber(clip.width),
    height: toNumber(clip.height),
    fps: toNumber(clip.fps),
    license_class: String(clip.license_class || clip.licenseClass || ''),
    raw_metadata: rawMetadata && typeof rawMetadata === 'object' ? rawMetadata : {},
    raw,
  };
}

export function findLargestClipArray(value, depth = 0) {
  if (depth > 8 || value == null) {
    return [];
  }

  if (Array.isArray(value)) {
    const clipLike = value.filter((item) => {
      if (!item || typeof item !== 'object') {
        return false;
      }

      return Boolean(
        item.id ||
        item.clipId ||
        item._id ||
        item.assetId ||
        item.title ||
        item.name ||
        item.previewUrl ||
        item.preview_url ||
        item.url ||
        item.src,
      );
    });

    if (clipLike.length >= 2) {
      return clipLike;
    }

    for (const item of value) {
      const nested = findLargestClipArray(item, depth + 1);
      if (nested.length) {
        return nested;
      }
    }

    return [];
  }

  if (typeof value === 'object') {
    for (const nestedValue of Object.values(value)) {
      const nested = findLargestClipArray(nestedValue, depth + 1);
      if (nested.length) {
        return nested;
      }
    }
  }

  return [];
}
