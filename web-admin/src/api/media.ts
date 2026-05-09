import { apiFetch } from './client';
import { makeMockItems } from '../data/mockMedia';
import type { ClipPayload, MediaItem, MediaSource } from '../lib/types';
import { asArray } from '../lib/utils';

type ApiClipResponse = {
  ok?: boolean;
  source?: string;
  clips?: unknown[];
  items?: unknown[];
  count?: number;
};

function normalizeClip(raw: any, source: MediaSource): MediaItem {
  return {
    id: String(raw.id ?? raw.clip_id ?? crypto.randomUUID()),
    source: String(raw.source ?? source),
    name: raw.name ?? raw.title ?? raw.filename ?? 'Untitled asset',
    filename: raw.filename,
    category: raw.category,
    tags: asArray(raw.tags),
    search_terms: asArray(raw.search_terms),
    status: raw.status,
    error: raw.error,
    external_url: raw.external_url,
    drive_link: raw.drive_link,
    drive_file_id: raw.drive_file_id,
    download_link: raw.download_link,
    local_path: raw.local_path,
    file_hash: raw.file_hash,
    folder_id: raw.folder_id,
    folder_path: raw.folder_path,
    duration: raw.duration,
    metadata: raw.metadata,
    created_at: raw.created_at,
    updated_at: raw.updated_at,
    thumb_url: raw.thumb_url,
    preview_url: raw.preview_url,
    is_folder: Boolean(raw.is_folder),
  };
}

export async function listMedia(source: MediaSource, q = '', limit = 100, offset = 0): Promise<MediaItem[]> {
  try {
    const params = new URLSearchParams();
    if (q) params.set('q', q);
    params.set('limit', String(limit));
    params.set('offset', String(offset));
    
    const data = await apiFetch<ApiClipResponse>(`/api/media/${source}/clips?${params.toString()}`);
    const list = data.clips ?? data.items ?? [];
    return list.map((item) => normalizeClip(item, source));
  } catch (error) {
    console.warn('Using mock media because backend is not reachable:', error);
    return makeMockItems(source).filter((item: MediaItem) => {
      const needle = q.toLowerCase();
      return !needle || [item.name, item.category, item.tags.join(' ')].join(' ').toLowerCase().includes(needle);
    }).slice(offset, offset + limit);
  }
}

export async function updateMedia(source: MediaSource, id: string, payload: ClipPayload) {
  return apiFetch(`/api/media/${source}/clips/${id}`, {
    method: 'PATCH',
    body: JSON.stringify(payload),
  });
}

export async function createMedia(source: MediaSource, payload: ClipPayload) {
  return apiFetch(`/api/media/${source}/clips`, {
    method: 'POST',
    body: JSON.stringify(payload),
  });
}

export async function verifyMedia(source: MediaSource, id: string) {
  return apiFetch(`/api/media/${source}/clips/${id}/verify`, { method: 'POST' });
}

export async function reprocessMedia(source: MediaSource, id: string) {
  return apiFetch(`/api/media/${source}/clips/${id}/reprocess`, {
    method: 'POST',
    body: JSON.stringify({ force: true, upload_drive: true, normalize: true }),
  });
}

export async function reuploadMedia(source: MediaSource, id: string) {
  return apiFetch(`/api/media/${source}/clips/${id}/reupload`, { method: 'POST' });
}

export async function trashMedia(source: MediaSource, id: string) {
  return apiFetch(`/api/media/${source}/clips/${id}/trash`, { method: 'POST' });
}

export async function deleteMedia(source: MediaSource, id: string) {
  return apiFetch(`/api/media/${source}/clips/${id}/delete`, { method: 'POST' });
}

export async function findDuplicates(source: MediaSource, id: string) {
  return apiFetch<{
    ok: boolean;
    source: string;
    clip_id: string;
    file_hash: string;
    duplicates: Array<{
      source: string;
      id: string;
      name: string;
      drive_link: string;
      local_path: string;
      thumb_url: string;
    }>;
  }>(`/api/media/${source}/clips/${id}/duplicates`);
}

export async function bulkReprocessMedia(source: MediaSource, ids: string[]) {
  return Promise.all(ids.map((id) => reprocessMedia(source, id)));
}

export async function bulkReuploadMedia(source: MediaSource, ids: string[]) {
  return Promise.all(ids.map((id) => reuploadMedia(source, id)));
}

export async function bulkTrashMedia(source: MediaSource, ids: string[]) {
  return Promise.all(ids.map((id) => trashMedia(source, id)));
}

export async function syncImages() {
  return apiFetch<{ ok: boolean; message: string }>('/api/images/sync', { method: 'POST' });
}
