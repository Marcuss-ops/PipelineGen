import { Trash2, Tags, RefreshCw } from 'lucide-react';
import { Button } from '../components/ui/Button';
import type { MediaItem } from '../lib/types';

interface MediaBulkActionsBarProps {
  selectedCount: number;
  onDeselectAll: () => void;
  onReprocess: () => void;
  onReupload: () => void;
  onAddTags: () => void;
  onTrash: () => void;
}

export function MediaBulkActionsBar({
  selectedCount,
  onDeselectAll,
  onReprocess,
  onReupload,
  onAddTags,
  onTrash,
}: MediaBulkActionsBarProps) {
  if (selectedCount === 0) return null;

  return (
    <div className="flex items-center justify-between rounded-2xl border border-blue-200 bg-blue-50 px-4 py-3 shadow-sm">
      <p className="text-sm font-semibold text-blue-950">{selectedCount} clip selezionate</p>
      <div className="flex gap-2">
        <Button variant="secondary" onClick={onDeselectAll}>Deseleziona</Button>
        <Button variant="secondary" onClick={onReprocess}>Reprocess</Button>
        <Button variant="secondary" onClick={onReupload}>Reupload</Button>
        <Button variant="secondary" onClick={onAddTags}>
          <Tags className="h-4 w-4" /> Tag
        </Button>
        <Button variant="danger" onClick={onTrash}>
          <Trash2 className="h-4 w-4" /> Delete
        </Button>
      </div>
    </div>
  );
}
