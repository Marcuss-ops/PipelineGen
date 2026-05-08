import { apiFetch } from './client';

export type ArtlistRunPayload = {
  term: string;
  limit: number;
  strategy: 'verify' | 'skip' | 'replace';
  dry_run?: boolean;
  root_folder_id?: string;
  clip_duration?: number;
  width?: number;
  height?: number;
  fps?: number;
};

export async function runArtlistPipeline(payload: ArtlistRunPayload) {
  return apiFetch('/api/artlist/run', {
    method: 'POST',
    body: JSON.stringify(payload),
  });
}
