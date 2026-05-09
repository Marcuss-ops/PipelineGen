import { Check, Cloud, Edit3, ExternalLink, RotateCcw, Trash2, UploadCloud, Folder as FolderIcon } from 'lucide-react';
import type { MediaItem } from '../lib/types';
import { Badge } from './ui/Badge';
import { Button } from './ui/Button';
import { formatDate } from '../lib/utils';
import { VideoThumbnail } from './VideoThumbnail';

export function MediaTable({
  items,
  selected,
  onSelect,
  onSelectAll,
  onOpen,
  onEdit,
  onVerify,
  onReprocess,
  onReupload,
  onTrash,
  onFolderClick,
}: {
  items: MediaItem[];
  selected: Set<string>;
  onSelect: (id: string, checked: boolean) => void;
  onSelectAll: (checked: boolean) => void;
  onOpen: (item: MediaItem) => void;
  onEdit: (item: MediaItem) => void;
  onVerify: (item: MediaItem) => void;
  onReprocess: (item: MediaItem) => void;
  onReupload: (item: MediaItem) => void;
  onTrash: (item: MediaItem) => void;
  onFolderClick?: (id: string) => void;
}) {
  const allSelected = items.length > 0 && items.every((item) => selected.has(item.id));

  return (
    <div className="overflow-hidden rounded-2xl border border-zinc-200 bg-white shadow-sm dark:border-zinc-800 dark:bg-zinc-900">
      <div className="grid grid-cols-[40px_72px_1fr_160px_180px_260px] items-center gap-6 border-b border-zinc-200 bg-zinc-50 px-6 py-4 text-xs font-bold uppercase tracking-wide text-zinc-500 max-xl:grid-cols-[40px_72px_1fr_180px] max-lg:grid-cols-[40px_60px_1fr_120px] dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-400">
        <input type="checkbox" checked={allSelected} onChange={(event) => onSelectAll(event.target.checked)} className="h-4 w-4 rounded border-zinc-300" />
        <span>Preview</span>
        <span>Asset / Folder</span>
        <span className="max-xl:hidden">Coerenza</span>
        <span className="max-lg:hidden">Aggiornato</span>
        <span className="text-right">Azioni</span>
      </div>
      {items.length === 0 ? (
        <div className="py-16 text-center text-sm text-zinc-500">Nessun elemento trovato</div>
      ) : (
        <div className="divide-y divide-zinc-100 dark:divide-zinc-800">
          {items.map((item) => (
            <div key={item.id} className="grid grid-cols-[40px_72px_1fr_160px_180px_260px] items-center gap-6 px-6 py-5 transition hover:bg-zinc-50 dark:hover:bg-zinc-800/50 max-xl:grid-cols-[40px_72px_1fr_180px] max-lg:grid-cols-[40px_60px_1fr_120px]">
              <input type="checkbox" checked={selected.has(item.id)} onChange={(event) => onSelect(item.id, event.target.checked)} className="h-4 w-4 rounded border-zinc-300" />
              <button
                onClick={() => item.is_folder && onFolderClick ? onFolderClick(item.id) : onOpen(item)}
                className="h-20 w-20 flex items-center justify-center bg-zinc-50 rounded-2xl border border-zinc-100 dark:bg-zinc-800 dark:border-zinc-700"
              >
                {item.is_folder ? (
                  <FolderIcon className="h-10 w-10 text-blue-500" />
                ) : item.source === 'images' ? (
                  <img
                    src={
                      item.thumb_url ||
                      item.preview_url ||
                      (item.drive_file_id
                        ? `https://drive.google.com/thumbnail?id=${item.drive_file_id}&sz=w400`
                        : `https://placehold.co/112x112?text=images`)
                    }
                    className="h-20 w-20 rounded-2xl object-cover ring-2 ring-zinc-900/5"
                    alt=""
                  />
                ) : (item.filename || '').toLowerCase().endsWith('.txt') || (item.source === 'voiceover' && !(item.filename || '').toLowerCase().endsWith('.mp3')) ? (
                  <div className="h-20 w-20 flex flex-col items-center justify-center bg-zinc-100 rounded-2xl border border-zinc-200 text-[10px] font-black text-zinc-400 uppercase tracking-tighter dark:bg-zinc-800 dark:border-zinc-700 dark:text-zinc-500">
                    <span className="text-lg mb-[-4px]">📄</span>
                    Txt
                  </div>
                ) : (item.filename || '').toLowerCase().endsWith('.mp3') || (item.filename || '').toLowerCase().endsWith('.wav') || (item.filename || '').toLowerCase().endsWith('.m4a') ? (
                  <div className="h-20 w-20 flex flex-col items-center justify-center bg-blue-50 rounded-2xl border border-blue-100 text-[10px] font-black text-blue-400 uppercase tracking-tighter dark:bg-blue-900/30 dark:border-blue-800 dark:text-blue-500">
                    <span className="text-lg mb-[-4px]">🎵</span>
                    Audio
                  </div>
                ) : (
                  <VideoThumbnail item={item} className="h-20 w-20 rounded-2xl" />
                )}
              </button>
              <div className="min-w-0">
                <div className="flex items-center gap-2">
                  <button
                    onClick={() => item.is_folder && onFolderClick ? onFolderClick(item.id) : onOpen(item)}
                    className={`block truncate text-left text-sm font-bold hover:text-blue-600 dark:hover:text-blue-400 ${item.is_folder ? 'text-blue-700 dark:text-blue-400' : 'text-zinc-900 dark:text-zinc-100'}`}
                  >
                    {item.is_folder && <span className="mr-1">📁</span>}
                    {item.name}
                  </button>
                </div>
                <div className="mt-1 flex flex-wrap gap-1.5">
                  {item.category && <Badge>{item.category}</Badge>}
                  {!item.is_folder && item.tags.slice(0, 3).map((tag) => <Badge key={tag} className="bg-zinc-50 dark:bg-zinc-800 dark:text-zinc-400 dark:border-zinc-700">{tag}</Badge>)}
                  {item.status && <Badge className={item.status.includes('missing') || item.status.includes('failed') ? 'bg-red-50 text-red-700 dark:bg-red-950/30 dark:text-red-400 dark:border-red-900/50' : 'bg-emerald-50 text-emerald-700 dark:bg-emerald-950/30 dark:text-emerald-400 dark:border-emerald-900/50'}>{item.status}</Badge>}
                </div>
                <div className="mt-1 truncate text-xs text-zinc-400">
                  {item.folder_path ? `${item.folder_path} / ` : ''}{item.filename || item.id}
                </div>
              </div>
              <div className="space-y-1 text-xs max-xl:hidden">
                <div className="flex items-center gap-2 text-zinc-600"><Cloud className="h-3.5 w-3.5" /> {item.drive_link ? 'Drive ok' : 'Drive mancante'}</div>
                {!item.is_folder && <div className="flex items-center gap-2 text-zinc-600"><Check className="h-3.5 w-3.5" /> {item.file_hash ? 'Hash ok' : 'Hash mancante'}</div>}
              </div>
              <div className="text-xs text-zinc-500 max-lg:hidden">{formatDate(item.updated_at)}</div>
              <div className="flex justify-end gap-1">
                {item.drive_link && <Button variant="ghost" size="sm" onClick={() => window.open(item.drive_link, '_blank')} title="Apri Drive"><ExternalLink className="h-4 w-4" /></Button>}
                {!item.is_folder && (
                  <>
                    <Button variant="ghost" size="sm" onClick={() => onEdit(item)} title="Modifica"><Edit3 className="h-4 w-4" /></Button>
                    <Button variant="ghost" size="sm" onClick={() => onVerify(item)} title="Verifica"><Check className="h-4 w-4" /></Button>
                    <Button variant="ghost" size="sm" onClick={() => onReupload(item)} title="Reupload"><UploadCloud className="h-4 w-4" /></Button>
                    <Button variant="ghost" size="sm" onClick={() => onReprocess(item)} title="Reprocess"><RotateCcw className="h-4 w-4" /></Button>
                  </>
                )}
                <Button variant="ghost" size="sm" onClick={() => onTrash(item)} title="Trash"><Trash2 className="h-4 w-4 text-red-600" /></Button>
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

