import { apiFetch } from './client';
import type { MediaSource } from '../lib/types';

export interface AssetNode {
  id: string;
  source: string;
  asset_id: string;
  name: string;
  type: 'folder' | 'video' | 'audio' | 'image' | 'file';
  parent_id: string;
  root_id: string;
  path: string;
  depth: number;
  is_folder: boolean;
  drive_file_id: string;
  drive_link: string;
  metadata: string;
  created_at: string;
  updated_at: string;
}

export interface TreeResponse {
  ok: boolean;
  source: string;
  children: AssetNode[];
}

export interface BreadcrumbResponse {
  ok: boolean;
  source: string;
  breadcrumb: AssetNode[];
}

export async function getTree(source: MediaSource, parentID: string = 'root'): Promise<AssetNode[]> {
  const data = await apiFetch<TreeResponse>(`/api/assets/${source}/tree?parent_id=${parentID}`);
  return data.children || [];
}

export async function getBreadcrumb(source: MediaSource, id: string): Promise<AssetNode[]> {
  const data = await apiFetch<BreadcrumbResponse>(`/api/assets/${source}/breadcrumb?id=${id}`);
  return data.breadcrumb || [];
}
