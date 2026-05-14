import { useMutation, useQuery, useQueries, useQueryClient, useInfiniteQuery } from '@tanstack/react-query';
import { useMemo, useState, useEffect } from 'react';
import { bulkAddTags, bulkRemoveTags, cleanupOrphans, bulkReprocessMedia, bulkReuploadMedia, bulkTrashMedia, deleteMedia, listMedia, reprocessMedia, reuploadMedia, trashMedia, updateMedia, verifyMedia, syncImages, searchLive } from '../api/media';
import { getTree, getBreadcrumb, type AssetNode } from '../api/assets';
import type { ClipPayload, MediaItem, MediaSource } from '../lib/types';
import { SOURCES } from '../lib/sources';

type SetBreadcrumb = React.Dispatch<React.SetStateAction<AssetNode[]>>;
type SetLiveResults = React.Dispatch<React.SetStateAction<any>>;
type SetNotice = React.Dispatch<React.SetStateAction<string>>;
type SetSelected = React.Dispatch<React.SetStateAction<Set<string>>>;

export function useMediaAdminState() {
  const [search, setSearch] = useState('');
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [editing, setEditing] = useState<MediaItem | null>(null);
  const [isLiveSearch, setIsLiveSearch] = useState(false);
  const [liveResults, setLiveResults] = useState<any>(null);
  const [notice, setNotice] = useState<string>('');
  const [activeFolderId, setActiveFolderId] = useState<string>('root');
  const [breadcrumb, setBreadcrumb] = useState<AssetNode[]>([]);

  return {
    search, setSearch,
    selected, setSelected,
    editing, setEditing,
    isLiveSearch, setIsLiveSearch,
    liveResults, setLiveResults,
    notice, setNotice,
    activeFolderId, setActiveFolderId,
    breadcrumb, setBreadcrumb,
  };
}

export function useMediaQueries(source: MediaSource, search: string, isLiveSearch: boolean, activeFolderId: string, setBreadcrumb: SetBreadcrumb) {
  const queryClient = useQueryClient();

  const treeQuery = useQuery({
    queryKey: ['tree', source, activeFolderId],
    queryFn: () => getTree(source, activeFolderId),
    enabled: search === '',
  });

  useEffect(() => {
    if (activeFolderId === 'root') {
      setBreadcrumb([]);
      return;
    }
    getBreadcrumb(source, activeFolderId).then(setBreadcrumb).catch(() => setBreadcrumb([]));
  }, [source, activeFolderId, setBreadcrumb]);

  const mediaQuery = useInfiniteQuery({
    queryKey: ['media', source, search],
    queryFn: ({ pageParam = 0 }) => listMedia(source, search, 50, pageParam).then(res => res.items),
    initialPageParam: 0,
    getNextPageParam: (lastPage, allPages) => {
      if (lastPage.length < 50) return undefined;
      return allPages.length * 50;
    },
    enabled: search !== '' && !isLiveSearch,
  });

  const allSourceQueries = useQueries({
    queries: SOURCES.map((s) => ({
      queryKey: ['media-count', s.id],
      queryFn: () => listMedia(s.id as MediaSource, '', 1),
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
  }, [allSourceQueries]);

  return {
    treeQuery,
    mediaQuery,
    allSourceQueries,
    allSourceCounts,
    refresh: () => {
      queryClient.invalidateQueries({ queryKey: ['media'] });
      queryClient.invalidateQueries({ queryKey: ['tree'] });
    },
  };
}

export function useMediaActions(
  source: MediaSource, 
  refresh: () => void, 
  setNotice: SetNotice, 
  setEditing: (item: MediaItem | null) => void, 
  setSelected: SetSelected, 
  selected: Set<string>
) {
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

  const handleBulkTrash = (filteredItems: MediaItem[]) => {
    filteredItems.forEach((item) => actionMutation.mutate({ action: 'trash', item }));
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

  const handleLiveSearch = async (searchTerm: string) => {
    if (!searchTerm) return null;
    setNotice('Ricerca live in corso...');
    try {
      const results = await searchLive(searchTerm);
      return results;
    } catch (err) {
      setNotice(`Errore ricerca live: ${err}`);
      return null;
    }
  };

  return {
    updateMutation,
    actionMutation,
    handleBulkTrash,
    handleBulkReprocess,
    handleBulkReupload,
    handleBulkTags,
    handleCleanupOrphans,
    syncMutation,
    handleLiveSearch,
  };
}
