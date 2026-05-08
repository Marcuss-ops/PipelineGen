import { Check, Cloud, Edit3, ExternalLink, RotateCcw, Trash2, UploadCloud } from 'lucide-react';
import type { MediaItem } from '../lib/types';
import { Badge } from './ui/Badge';
import { Button } from './ui/Button';
import { formatDate } from '../lib/utils';

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
}) {
  const allSelected = items.length > 0 && items.every((item) => selected.has(item.id));

  return (
    <div className="overflow-hidden rounded-2xl border border-zinc-200 bg-white shadow-sm">
      <div className="grid grid-cols-[40px_72px_1fr_160px_180px_260px] items-center gap-6 border-b border-zinc-200 bg-zinc-50 px-6 py-4 text-xs font-bold uppercase tracking-wide text-zinc-500 max-xl:grid-cols-[40px_72px_1fr_180px] max-lg:grid-cols-[40px_60px_1fr_120px]">
        <input type="checkbox" checked={allSelected} onChange={(event) => onSelectAll(event.target.checked)} className="h-4 w-4 rounded border-zinc-300" />
        <span>Preview</span>
        <span>Asset</span>
        <span className="max-xl:hidden">Coerenza</span>
        <span className="max-lg:hidden">Aggiornato</span>
        <span className="text-right">Azioni</span>
      </div>
      {items.length === 0 ? (
        <div className="py-16 text-center text-sm text-zinc-500">Nessun elemento trovato</div>
      ) : (
        <div className="divide-y divide-zinc-100">
          {items.map((item) => (
            <div key={item.id} className="grid grid-cols-[40px_72px_1fr_160px_180px_260px] items-center gap-6 px-6 py-5 transition hover:bg-zinc-50 max-xl:grid-cols-[40px_72px_1fr_180px] max-lg:grid-cols-[40px_60px_1fr_120px]">
              <input type="checkbox" checked={selected.has(item.id)} onChange={(event) => onSelect(item.id, event.target.checked)} className="h-4 w-4 rounded border-zinc-300" />
              <button onClick={() => onOpen(item)} className="h-20 w-20 overflow-hidden rounded-2xl bg-zinc-100 ring-2 ring-zinc-900/5">
                <img src={item.thumb_url || `https://placehold.co/112x112?text=${encodeURIComponent(item.source)}`} className="h-full w-full object-cover" alt="" />
              </button>
              <div className="min-w-0">
                <button onClick={() => onOpen(item)} className="block max-w-full truncate text-left text-sm font-bold text-zinc-900 hover:text-blue-600">{item.name}</button>
                <div className="mt-1 flex flex-wrap gap-1.5">
                  {item.category && <Badge>{item.category}</Badge>}
                  {item.tags.slice(0, 3).map((tag) => <Badge key={tag} className="bg-zinc-50">{tag}</Badge>)}
                  {item.status && <Badge className={item.status.includes('missing') || item.status.includes('failed') ? 'bg-red-50 text-red-700' : 'bg-emerald-50 text-emerald-700'}>{item.status}</Badge>}
                </div>
                <div className="mt-1 truncate text-xs text-zinc-500">{item.filename || item.external_url || item.id}</div>
              </div>
              <div className="space-y-1 text-xs max-xl:hidden">
                <div className="flex items-center gap-2 text-zinc-600"><Cloud className="h-3.5 w-3.5" /> {item.drive_link ? 'Drive ok' : 'Drive mancante'}</div>
                <div className="flex items-center gap-2 text-zinc-600"><Check className="h-3.5 w-3.5" /> {item.file_hash ? 'Hash ok' : 'Hash mancante'}</div>
              </div>
              <div className="text-xs text-zinc-500 max-lg:hidden">{formatDate(item.updated_at)}</div>
              <div className="flex justify-end gap-1">
                {item.drive_link && <Button variant="ghost" size="sm" onClick={() => window.open(item.drive_link, '_blank')} title="Apri Drive"><ExternalLink className="h-4 w-4" /></Button>}
                <Button variant="ghost" size="sm" onClick={() => onEdit(item)} title="Modifica"><Edit3 className="h-4 w-4" /></Button>
                <Button variant="ghost" size="sm" onClick={() => onVerify(item)} title="Verifica"><Check className="h-4 w-4" /></Button>
                <Button variant="ghost" size="sm" onClick={() => onReupload(item)} title="Reupload"><UploadCloud className="h-4 w-4" /></Button>
                <Button variant="ghost" size="sm" onClick={() => onReprocess(item)} title="Reprocess"><RotateCcw className="h-4 w-4" /></Button>
                <Button variant="ghost" size="sm" onClick={() => onTrash(item)} title="Trash"><Trash2 className="h-4 w-4 text-red-600" /></Button>
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
