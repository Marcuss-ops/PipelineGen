import fs from 'node:fs/promises';
import path from 'node:path';

const DEFAULT_REGISTRY_PATH = path.join(process.cwd(), 'config', 'artlist-endpoints.json');

export function resolveArtlistEndpointRegistryPath(explicitPath = process.env.ARTLIST_ENDPOINT_REGISTRY || '') {
  const trimmed = String(explicitPath || '').trim();
  return trimmed || DEFAULT_REGISTRY_PATH;
}

export async function loadArtlistEndpointRegistry(registryPath = resolveArtlistEndpointRegistryPath()) {
  try {
    const raw = await fs.readFile(registryPath, 'utf8');
    const parsed = JSON.parse(raw);

    if (!parsed || typeof parsed !== 'object') {
      throw new Error('registry JSON must be an object');
    }

    return parsed;
  } catch (err) {
    if (err && err.code === 'ENOENT') {
      return null;
    }
    const detail = err && err.message ? err.message : String(err);
    throw new Error(`failed to load artlist endpoint registry from ${registryPath}: ${detail}`);
  }
}

export function getFootageSearchEndpoint(registry) {
  if (!registry || typeof registry !== 'object') {
    return null;
  }

  const endpoint = registry.footage_search;
  if (!endpoint || typeof endpoint !== 'object') {
    return null;
  }

  if (!endpoint.enabled) {
    return null;
  }

  if (typeof endpoint.url !== 'string' || !endpoint.url.trim()) {
    const err = new Error('footage_search endpoint is missing url');
    err.code = 'ARTLIST_ENDPOINT_INVALID';
    throw err;
  }

  return {
    method: (endpoint.method || 'POST').toUpperCase(),
    url: endpoint.url.trim(),
    kind: endpoint.kind || 'graphql',
    operationName: endpoint.operation_name || endpoint.operationName || '',
    enabled: true,
  };
}
