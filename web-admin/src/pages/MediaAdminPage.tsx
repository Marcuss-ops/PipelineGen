import { useMutation, useQuery, useQueries, useQueryClient } from '@tanstack/react-query';
import { Plus, Search, Trash2, RefreshCw } from 'lucide-react';
import { useMemo, useState } from 'react';
import { bulkReprocessMedia, bulkReuploadMedia, bulkTrashMedia, deleteMedia, listMedia, reprocessMedia, reuploadMedia, trashMedia, updateMedia, verifyMedia, syncImages } from '../api/media';
import { MediaDetailDrawer } from '../components/MediaDetailDrawer';
import { MediaTable } from '../components/MediaTable';
import { SourceTabs } from '../components/SourceTabs';
import { StatsGrid, type FilterType } from '../components/StatsGrid';
import { Button } from '../components/ui/Button';
import { SOURCES, sourceById } from '../lib/sources';
import type { ClipPayload, MediaItem, MediaSource } from '../lib/types';

export function MediaAdminPage() {
  const queryClient = useQueryClient();
  const [source, setSource] = useState<MediaSource>('artlist');
  const [search, setSearch] = useState('');
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [editing, setEditing] = useState<MediaItem | null>(null);
  const [notice, setNotice] = useState<string>('');
  const [activeFilter, setActiveFilter] = useState<FilterType>('all');

  const mediaQuery = useQuery({
    queryKey: ['media', source, search],
    queryFn: () => listMedia(source, search),
  });

  const allSourceQueries = useQueries({
    queries: SOURCES.map((s) => ({
      queryKey: ['media-count', s.id],
      queryFn: () => listMedia(s.id as MediaSource, ''),
      staleTime: 60000,
    })),
  });

  const allSourceCounts = useMemo(() => {
    const counts: Record<string, number> = {};
    SOURCES.forEach((s, idx) => {
      const q = allSourceQueries[idx];
      counts[s.id] = q.data?.length ?? 0;
    });
    return counts;
  }, [allSourceQueries.map((q) => q.data).join(','), SOURCES]);

  const items = mediaQuery.data ?? [];
  const filteredItems = useMemo(() => {
    if (activeFilter === 'processed') return items.filter((item) => item.drive_link || item.download_link);
    if (activeFilter === 'missingDrive') return items.filter((item) => !item.drive_link && !item.download_link);
    if (activeFilter === 'missingHash') return items.filter((item) => !item.file_hash);
    if (activeFilter === 'noThumbnail') return items.filter((item) => !item.thumb_url);
    if (activeFilter === 'localOnly') return items.filter((item) => item.local_path && !item.drive_link);
    if (activeFilter === 'withErrors') return items.filter((item) => Boolean(item.error) || String(item.status || '').includes('failed'));
    return items;
  }, [items, activeFilter]);
  const selectedItems = filteredItems.filter((item) => selected.has(item.id));

  const refresh = () => queryClient.invalidateQueries({ queryKey: ['media'] });

  const updateMutation = useMutation({
    mutationFn: ({ id, payload }: { id: string; payload: ClipPayload }) => updateMedia(source, id, payload),
    onSuccess: () => { setNotice('Asset aggiornato'); setEditing(null); refresh(); },
    onError: (error) => setNotice(`Backend non disponibile o endpoint mancante: ${String(error)}`),
  });

  const actionMutation = useMutation({
    mutationFn: async ({ action, item }: { action: 'verify' | 'reprocess' | 'reupload' | 'trash' | 'delete'; item: MediaItem }) => {
      if (action === 'verify') return verifyMedia(source, item.id);
      if (action === 'reprocess') return reprocessMedia(source, item.id);
      if (action === 'reupload') return reuploadMedia(source, item.id);
      if (action === 'delete') return deleteMedia(source, item.id);
      return trashMedia(source, item.id);
    },
    onSuccess: (_, vars) => { setNotice(`Azione completata: ${vars.action}`); refresh(); },
    onError: (error) => setNotice(`Azione non riuscita: ${String(error)}`),
  });

  const handleBulkTrash = () => {
    selectedItems.forEach((item) => actionMutation.mutate({ action: 'trash', item }));
    setSelected(new Set());
  };

  const handleBulkReprocess = async () => {
    const ids = Array.from(selected);
    await bulkReprocessMedia(source, ids);
    setNotice(`Reprocessing ${ids.length} clip`);
    setSelected(new Set());
    refresh();
  };

  const handleBulkReupload = async () => {
    const ids = Array.from(selected);
    await bulkReuploadMedia(source, ids);
    setNotice(`Reuploading ${ids.length} clip`);
    setSelected(new Set());
    refresh();
  };

  const syncMutation = useMutation({
    mutationFn: syncImages,
    onSuccess: (data) => {
      setNotice(data.message || 'Sincronizzazione completata');
      refresh();
    },
    onError: (err) => setNotice(`Errore sinc: ${err}`),
  });

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-black tracking-tight text-zinc-900">Media Database</h1>
        <div className="flex gap-2">
          {source === 'images' && (
            <Button variant="secondary" onClick={() => syncMutation.mutate()} isLoading={syncMutation.isPending}>
              <RefreshCw className={`h-4 w-4 ${syncMutation.isPending ? 'animate-spin' : ''}`} /> Sincronizza Drive
            </Button>
          )}
          <Button onClick={() => setEditing({ id: crypto.randomUUID(), source, name: 'Nuovo asset', tags: [], status: 'draft' })}>
            <Plus className="h-4 w-4" /> Aggiungi asset
          </Button>
        </div>
      </div>

      <section className="rounded-3xl border border-zinc-200 bg-white p-8 shadow-sm">
        <SourceTabs active={source} counts={allSourceCounts} onChange={(next) => { setSource(next); setSelected(new Set()); setActiveFilter('all'); }} />
        <div className="mt-5 flex flex-col gap-3 md:flex-row md:items-center md:justify-between">
          <div>
            <h3 className="text-lg font-bold">{sourceById[source].label}</h3>
            <p className="text-sm text-zinc-500">{sourceById[source].description}</p>
          </div>
          <div className="relative w-full md:max-w-md">
            <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-zinc-400" />
            <input value={search} onChange={(event) => setSearch(event.target.value)} placeholder={`Cerca in ${sourceById[source].label}...`} className="h-10 w-full rounded-xl border border-zinc-200 bg-zinc-50 pl-9 pr-3 text-sm outline-none transition focus:border-blue-500 focus:bg-white focus:ring-4 focus:ring-blue-500/10" />
          </div>
        </div>
      </section>

      {selected.size > 0 && (
        <div className="flex items-center justify-between rounded-2xl border border-blue-200 bg-blue-50 px-4 py-3 shadow-sm">
          <p className="text-sm font-semibold text-blue-950">{selected.size} clip selezionate</p>
          <div className="flex gap-2">
            <Button variant="secondary" onClick={() => setSelected(new Set())}>Deseleziona</Button>
            <Button variant="secondary" onClick={handleBulkReprocess}>Reprocess</Button>
            <Button variant="secondary" onClick={handleBulkReupload}>Reupload</Button>
            <Button variant="danger" onClick={handleBulkTrash}><Trash2 className="h-4 w-4" /> Delete</Button>
          </div>
        </div>
      )}

      {notice && <div className="rounded-2xl border border-zinc-200 bg-white px-4 py-3 text-sm text-zinc-600 shadow-sm">{notice}</div>}

      <MediaTable
        items={filteredItems}
        selected={selected}
        onSelect={(id, checked) => setSelected((previous) => { const next = new Set(previous); checked ? next.add(id) : next.delete(id); return next; })}
        onSelectAll={(checked) => setSelected(checked ? new Set(filteredItems.map((item) => item.id)) : new Set())}
        onOpen={setEditing}
        onEdit={setEditing}
        onVerify={(item) => actionMutation.mutate({ action: 'verify', item })}
        onReprocess={(item) => actionMutation.mutate({ action: 'reprocess', item })}
        onReupload={(item) => actionMutation.mutate({ action: 'reupload', item })}
        onTrash={(item) => actionMutation.mutate({ action: 'trash', item })}
      />

      <MediaDetailDrawer
        item={editing}
        open={Boolean(editing)}
        onClose={() => setEditing(null)}
        onSave={(payload) => {
          if (!editing) return;
          updateMutation.mutate({ id: editing.id, payload });
        }}
      />
    </div>
  );
}
