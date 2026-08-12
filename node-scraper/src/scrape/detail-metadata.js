// Compatibility façade for detail metadata extraction.
// Implementations are split by source and result responsibility while the
// existing import path remains the single public module for callers.
export {
  findClipObject,
  extractFromApiObject,
  extractFromNextData,
  extractFromJsonLd,
} from './detail-metadata-sources.js';
export { extractFromDom } from './detail-metadata-dom.js';
export { mergeMetadata, buildResult } from './detail-metadata-result.js';
