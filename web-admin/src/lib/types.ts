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
}

export interface ClipPayload {
  name?: string;
  tags?: string[];
  search_terms?: string[];
  status?: string;
}
