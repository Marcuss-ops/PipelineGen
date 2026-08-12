import { toString, toStringArray } from './detail-metadata-values.js';

export function findClipObject(obj) {
  if (obj == null || typeof obj !== 'object') return null;
  if ((obj.id || obj.clipId || obj._id) && (obj.title || obj.name) && (Array.isArray(obj.tags) || obj.description || obj.creator || obj.author)) {
    return obj;
  }
  if (Array.isArray(obj)) {
    for (const item of obj) {
      const found = findClipObject(item);
      if (found) return found;
    }
    return null;
  }
  for (const key of Object.keys(obj)) {
    if (key === '__next' || key === 'runtime' || key === 'env') continue;
    const found = findClipObject(obj[key]);
    if (found) return found;
  }
  return null;
}

export function extractFromApiObject(raw, clipPageUrl) {
  if (!raw) return {};
  return {
    title: toString(raw.title || raw.name),
    description: toString(raw.description),
    creator: toString(raw.creator || raw.author || raw.artist || raw.contributor),
    country: toString(raw.country || raw.countryName),
    location: toString(raw.location || raw.shootingLocation || raw.place),
    tags: toStringArray(raw.tags || raw.keywords),
    categories: toStringArray(raw.categories),
    thumbnail_url: toString(raw.thumbnailUrl || raw.thumbnail_url || raw.image),
    preview_url: toString(raw.previewUrl || raw.preview_url || raw.video),
    clip_page_url: toString(raw.clipPageUrl || raw.clip_page_url || clipPageUrl),
  };
}

export function extractFromNextData(nextData, clipPageUrl) {
  if (!nextData || typeof nextData !== 'object') return {};
  const props = nextData.props || nextData;
  const pageProps = props.pageProps || props;
  const initialProps = pageProps.initialProps || pageProps;
  const candidates = [
    pageProps.clip,
    pageProps.asset,
    pageProps.product,
    pageProps.data,
    initialProps.clip,
    initialProps.asset,
    initialProps.product,
    initialProps.data,
  ].filter(Boolean);

  for (const candidate of candidates) {
    if (typeof candidate !== 'object') continue;
    const found = findClipObject(candidate);
    if (found) {
      const meta = extractFromApiObject(found, clipPageUrl);
      if (meta.title) return meta;
    }
  }
  const found = findClipObject(pageProps);
  return found ? extractFromApiObject(found, clipPageUrl) : {};
}

export function extractFromJsonLd(jsonLdScripts, clipPageUrl) {
  const result = {};
  if (!Array.isArray(jsonLdScripts)) return result;
  for (const script of jsonLdScripts) {
    const html = script?.innerHTML || '';
    if (!html.trim()) continue;
    try {
      const data = JSON.parse(html);
      const graph = Array.isArray(data['@graph']) ? data['@graph'] : [data];
      for (const item of graph) {
        if (!item || typeof item !== 'object') continue;
        const type = item['@type'] || '';
        if (type === 'VideoObject' || item.name || item.description || item.contentUrl) {
          result.title = result.title || toString(item.name);
          result.description = result.description || toString(item.description);
          result.thumbnail_url = result.thumbnail_url || toString(item.thumbnailUrl || item.thumbnail?.url);
          result.preview_url = result.preview_url || toString(item.contentUrl || item.embedUrl);
          result.clip_page_url = result.clip_page_url || toString(item.url) || clipPageUrl;
        }
      }
    } catch {
      // Ignore malformed JSON-LD.
    }
  }
  return result;
}
