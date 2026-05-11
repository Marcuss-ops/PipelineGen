export type MediaSource = 'artlist' | 'youtube' | 'stock' | 'images' | 'voiceover';

export interface MediaItem {
  id: string;
  name: string;
  source: MediaSource;
  is_folder: boolean;
  type: 'folder' | 'file' | 'clip';
  drive_link?: string;
  download_link?: string;
  folder_path: string;
  tags: string[];
  search_terms: string[];
  created_at: string;
  updated_at: string;
  status: string;
  error?: string;
  
  filename?: string;
  category?: string;
  thumb_url?: string;
  preview_url?: string;
  drive_file_id?: string;
  local_path?: string;
  file_hash?: string;
  external_url?: string;
  folder_id?: string;
  metadata?: any;
  duration?: number;
  child_count?: number;
}

export interface ClipPayload {
  name?: string;
  filename?: string;
  category?: string;
  tags?: string[];
  search_terms?: string[];
  external_url?: string;
  drive_link?: string;
  drive_file_id?: string;
  download_link?: string;
  local_path?: string;
  file_hash?: string;
  folder_id?: string;
  folder_path?: string;
  metadata?: any;
  duration?: number;
  status?: string;
  error?: string;
}
