import { useMutation, useQuery, useQueries, useQueryClient, useInfiniteQuery } from '@tanstack/react-query';
import { Plus, Search, Trash2, RefreshCw, Folder, Tags, ShieldAlert } from 'lucide-react';
import { useMemo, useState, useEffect } from 'react';
import { bulkAddTags, bulkRemoveTags, cleanupOrphans, bulkReprocessMedia, bulkReuploadMedia, bulkTrashMedia, deleteMedia, listMedia, reprocessMedia, reuploadMedia, trashMedia, updateMedia, verifyMedia, syncImages } from '../api/media';
import { getTree, getBreadcrumb, type AssetNode } from '../api/assets';
import { MediaDetailDrawer } from '../components/MediaDetailDrawer';
import { MediaTable } from '../components/MediaTable';
import { SourceTabs } from '../components/SourceTabs';
import { StatsGrid, type FilterType } from '../components/StatsGrid';
import { Button } from '../components/ui/Button';
import { SOURCES, sourceById } from '../lib/sources';
import type { ClipPayload, MediaItem, MediaSource } from '../lib/types';
import { apiFetch } from '../api/client';
import { HierarchyNavigator } from '../components/HierarchyNavigator';
import { asArray, cn } from '../lib/utils';

type FolderInfo = { id: string; folder_path: string; clip_count: number };

export function MediaAdminPage() {
  const queryClient = useQueryClient();
  const [source, setSource] = useState<MediaSource>('artlist');
  const [search, setSearch] = useState('');
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [editing, setEditing] = useState<MediaItem | null>(null);
  const [notice, setNotice] = useState<string>('');
  const [activeFilter, setActiveFilter] = useState<FilterType>('all');
  const [activeFolderId, setActiveFolderId] = useState<string>('root');
  const [breadcrumb, setBreadcrumb] = useState<AssetNode[]>([]);

  // Tree query
  const treeQuery = useQuery({
    queryKey: ['tree', source, activeFolderId],
    queryFn: () => getTree(source, activeFolderId),
    enabled: search === '',
  });

  // Breadcrumb query
  useEffect(() => {
    if (activeFolderId === 'root') {
      setBreadcrumb([]);
      return;
    }
    getBreadcrumb(source, activeFolderId).then(setBreadcrumb).catch(() => setBreadcrumb([]));
  }, [source, activeFolderId]);

  const mediaQuery = useInfiniteQuery({
    queryKey: ['media', source, search],
    queryFn: ({ pageParam = 0 }) => listMedia(source, search, 50, pageParam).then(res => res.items),
    initialPageParam: 0,
    getNextPageParam: (lastPage, allPages) => {
      if (lastPage.length < 50) return undefined;
      return allPages.length * 50;
    },
    enabled: search !== '',
  });

  const allSourceQueries = useQueries({
    queries: SOURCES.map((s) => ({
      queryKey: ['media-count', s.id],
      queryFn: () => listMedia(s.id as MediaSource, '', 1), // Only fetch 1 item to get total count
      staleTime: 60000,
    })),
  });

  const allSourceCounts = useMemo(() => {
    const counts: Record<string, number> = {};
    SOURCES.forEach((s, idx) => {
      const q = allSourceQueries[idx];
      counts[s.id] = q.data?.total ?? 0;
    });
    return counts;
  }, [allSourceQueries, SOURCES]);

  const items = search === '' 
    ? (treeQuery.data || []) 
    : (mediaQuery.data?.pages.flat() || []);
  
  // Transform Tree items to MediaItems for compatibility with MediaTable
  const normalizedItems = useMemo(() => {
    return items.map(item => {
      const base = item as any;
      return {
        ...base,
        id: base.id,
        name: base.name,
        source: base.source || source,
        is_folder: base.is_folder ?? false,
        type: base.type || (base.is_folder ? 'folder' : 'file'),
        drive_link: base.drive_link,
        folder_path: base.path || base.folder_path,
        tags: asArray(base.tags),
        search_terms: asArray(base.search_terms),
        created_at: base.created_at || new Date().toISOString(),
        updated_at: base.updated_at || new Date().toISOString(),
        status: base.status || 'unknown'
      } as MediaItem;
    });
  }, [items, source]);

  const filteredItems = useMemo(() => {
    let result = normalizedItems;
    if (activeFilter === 'processed') result = result.filter((item) => item.drive_link || item.download_link);
    if (activeFilter === 'missingDrive') result = result.filter((item) => !item.drive_link && !item.download_link);
    if (activeFilter === 'withErrors') result = result.filter((item) => Boolean(item.error) || String(item.status || '').includes('failed'));
    return result;
  }, [normalizedItems, activeFilter]);

  const selectedItems = filteredItems.filter((item) => selected.has(item.id));

  const refresh = () => {
    queryClient.invalidateQueries({ queryKey: ['media'] });
    queryClient.invalidateQueries({ queryKey: ['tree'] });
  };

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

  const handleBulkTags = async () => {
    const input = window.prompt('Inserisci i tag separati da virgola (es: cinemmatic, alaska):');
    if (!input) return;
    const tags = input.split(',').map(s => s.trim()).filter(Boolean);
    const ids = Array.from(selected);
    await bulkAddTags(source, ids, tags);
    setNotice(`Aggiunti tag a ${ids.length} elementi`);
    setSelected(new Set());
    refresh();
  };

  const handleCleanupOrphans = async () => {
    if (!window.confirm('Vuoi cercare i file orfani (DB presente ma file rimosso su Drive)?')) return;
    setNotice('Ricerca orfani in corso...');
    try {
      const data = await cleanupOrphans(source, false);
      setNotice(`Pulizia completata: rimossi ${data.count} elementi orfani su ${data.checked} controllati.`);
      refresh();
    } catch (err) {
      setNotice(`Errore pulizia: ${err}`);
    }
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
            <Button variant="secondary" onClick={handleCleanupOrphans}>
              <ShieldAlert className="h-4 w-4" /> Pulisci Orfani
            </Button>
            {source === 'images' && (
              <Button variant="secondary" onClick={() => syncMutation.mutate()} disabled={syncMutation.isPending}>
                <RefreshCw className={`h-4 w-4 ${syncMutation.isPending ? 'animate-spin' : ''}`} /> Sincronizza Drive
              </Button>
            )}
            <Button onClick={() => setEditing({
            id: crypto.randomUUID(),
            source,
            name: 'Nuovo asset',
            tags: [],
            search_terms: [],
            is_folder: false,
            status: 'draft',
            created_at: new Date().toISOString(),
            updated_at: new Date().toISOString()
          } as MediaItem)}>
            <Plus className="h-4 w-4" /> Aggiungi asset
          </Button>
        </div>
      </div>


      <section className="rounded-3xl border border-zinc-200 bg-white p-8 shadow-sm dark:border-zinc-800 dark:bg-zinc-900">
        <SourceTabs active={source} counts={allSourceCounts} onChange={(next) => { setSource(next); setSelected(new Set()); setActiveFilter('all'); setActiveFolderId('root'); }} />
        <div className="mt-5 flex flex-col gap-3 md:flex-row md:items-center md:justify-between">
          <div>
            <h3 className="text-lg font-bold text-zinc-900 dark:text-zinc-100">{sourceById[source].label}</h3>
            <p className="text-sm text-zinc-500 dark:text-zinc-400">{sourceById[source].description}</p>
          </div>
          <div className="relative w-full md:max-w-md flex gap-2">
            <div className="relative flex-1">
              <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-zinc-400 dark:text-zinc-500" />
              <input value={search} onChange={(event) => setSearch(event.target.value)} placeholder={`Cerca in ${sourceById[source].label}...`} className="h-10 w-full rounded-xl border border-zinc-200 bg-zinc-50 pl-9 pr-3 text-sm outline-none transition focus:border-blue-500 focus:bg-white focus:ring-4 focus:ring-blue-500/10 dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-200 dark:focus:border-blue-500 dark:focus:bg-zinc-900" />
            </div>
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
            <Button variant="secondary" onClick={handleBulkTags}><Tags className="h-4 w-4" /> Tag</Button>
            <Button variant="danger" onClick={handleBulkTrash}><Trash2 className="h-4 w-4" /> Delete</Button>
          </div>
        </div>
      )}

      {notice && <div className="rounded-2xl border border-zinc-200 bg-white px-4 py-3 text-sm text-zinc-600 shadow-sm">{notice}</div>}

      {search === '' && (
        <HierarchyNavigator 
          breadcrumb={breadcrumb} 
          onNavigate={(id) => setActiveFolderId(id)} 
        />
      )}

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
        onFolderClick={(id) => setActiveFolderId(id)}
      />

      {search !== '' && mediaQuery.hasNextPage && (
        <div className="mt-8 flex justify-center">
          <Button 
            variant="secondary" 
            onClick={() => mediaQuery.fetchNextPage()} 
            disabled={mediaQuery.isFetchingNextPage}
            className="w-full max-w-xs"
          >
            {mediaQuery.isFetchingNextPage ? (
              <>
                <RefreshCw className="h-4 w-4 animate-spin" />
                Caricamento...
              </>
            ) : (
              'Carica altri asset'
            )}
          </Button>
        </div>
      )}

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

