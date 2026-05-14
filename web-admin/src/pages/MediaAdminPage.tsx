import { useMemo, useState } from 'react';
import { MediaDetailDrawer } from '../components/MediaDetailDrawer';
import { MediaTable } from '../components/MediaTable';
import { StatsGrid, type FilterType } from '../components/StatsGrid';
import type { MediaItem, MediaSource } from '../lib/types';
import { HierarchyNavigator } from '../components/HierarchyNavigator';
import { asArray } from '../lib/utils';
import { 
  useMediaAdminState, 
  useMediaQueries, 
  useMediaActions 
} from './media/useMediaAdminHooks';
import { MediaToolbar, MediaAdminHeader } from './media/MediaToolbar';
import { MediaBulkActionsBar } from './media/MediaBulkActionsBar';
import { LiveSearchResults } from './media/LiveSearchResults';

type FolderInfo = { id: string; folder_path: string; clip_count: number };

export function MediaAdminPage() {
  const [source, setSource] = useState<MediaSource>('artlist');
  const state = useMediaAdminState();
  const { search, setSearch, selected, setSelected, editing, setEditing, isLiveSearch, setIsLiveSearch, liveResults, setLiveResults, notice, setNotice, activeFolderId, setActiveFolderId, breadcrumb, setBreadcrumb } = state;
  
  const { treeQuery, mediaQuery, allSourceCounts, refresh } = useMediaQueries(source, search, isLiveSearch, activeFolderId, setBreadcrumb);
  const actions = useMediaActions(source, refresh, setNotice, setEditing, setSelected, selected);
  const { handleBulkTrash, handleBulkReprocess, handleBulkReupload, handleBulkTags, handleCleanupOrphans, syncMutation, handleLiveSearch } = actions;
  
  const [activeFilter, setActiveFilter] = useState<FilterType>('all');

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

  const handleAddAsset = () => {
    setEditing({
      id: crypto.randomUUID(),
      source,
      name: 'Nuovo asset',
      tags: [],
      search_terms: [],
      is_folder: false,
      type: 'clip',
      folder_path: 'root',
      status: 'draft',
      created_at: new Date().toISOString(),
      updated_at: new Date().toISOString()
    } as MediaItem);
  };

  const handleSourceChange = (next: MediaSource) => {
    setSource(next);
    setSelected(new Set());
    setActiveFilter('all');
    setActiveFolderId('root');
    setIsLiveSearch(false);
    setLiveResults(null);
  };

  const executeLiveSearch = async () => {
    const results = await handleLiveSearch(search);
    if (results) {
      setLiveResults(results);
      setNotice(`Ricerca completata: trovati ${results.youtube?.count || 0} video su YouTube`);
    }
  };

  return (
    <div className="space-y-6">
      <MediaAdminHeader 
        source={source}
        onCleanupOrphans={handleCleanupOrphans}
        onSyncImages={() => syncMutation.mutate()}
        onAddAsset={handleAddAsset}
        syncMutation={syncMutation}
      />

      <MediaToolbar
        source={source}
        search={search}
        isLiveSearch={isLiveSearch}
        activeFilter={activeFilter}
        allSourceCounts={allSourceCounts}
        onSourceChange={handleSourceChange}
        onSearchChange={setSearch}
        onLiveSearchToggle={() => setIsLiveSearch(!isLiveSearch)}
        onFilterChange={setActiveFilter}
        onCleanupOrphans={handleCleanupOrphans}
        onSyncImages={() => syncMutation.mutate()}
        onAddAsset={handleAddAsset}
        onLiveSearchExecute={executeLiveSearch}
        syncMutation={syncMutation}
      />

      <MediaBulkActionsBar
        selectedCount={selected.size}
        onDeselectAll={() => setSelected(new Set())}
        onReprocess={handleBulkReprocess}
        onReupload={handleBulkReupload}
        onAddTags={handleBulkTags}
        onTrash={handleBulkTrash.bind(null, filteredItems)}
      />

      {notice && <div className="rounded-2xl border border-zinc-200 bg-white px-4 py-3 text-sm text-zinc-600 shadow-sm">{notice}</div>}

      {isLiveSearch && liveResults && (
        <LiveSearchResults results={liveResults} onNotice={setNotice} />
      )}

      {!isLiveSearch && (
        <>
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
            onVerify={(item) => actions.actionMutation.mutate({ action: 'verify', item })}
            onReprocess={(item) => actions.actionMutation.mutate({ action: 'reprocess', item })}
            onReupload={(item) => actions.actionMutation.mutate({ action: 'reupload', item })}
            onTrash={(item) => actions.actionMutation.mutate({ action: 'trash', item })}
            onFolderClick={(id) => setActiveFolderId(id)}
          />

          {search !== '' && mediaQuery.hasNextPage && (
            <div className="mt-8 flex justify-center">
              <Button 
                variant="secondary" 
                onClick={() => mediaQuery.fetchNextPage()} 
                disabled={mediaQuery.isFetchingNextPage}
              >
                Carica altri risultati
              </Button>
            </div>
          )}
        </>
      )}

      <StatsGrid
        activeFilter={activeFilter}
        onFilterChange={setActiveFilter}
        counts={{
          total: normalizedItems.length,
          processed: normalizedItems.filter(i => i.drive_link || i.download_link).length,
          missingDrive: normalizedItems.filter(i => !i.drive_link && !i.download_link).length,
          withErrors: normalizedItems.filter(i => Boolean(i.error) || String(i.status || '').includes('failed')).length,
        }}
      />

      {editing && (
        <MediaDetailDrawer
          open={!!editing}
          onClose={() => setEditing(null)}
          item={editing}
          onSave={(payload) => actions.updateMutation.mutate({ id: editing.id, payload })}
          isLoading={actions.updateMutation.isPending}
        />
      )}
    </div>
  );
}
