import { AlertTriangle, CheckCircle2, Cloud, Database, EyeOff, ImageOff, Loader2, Server } from 'lucide-react';
import type { MediaItem } from '../lib/types';

export type FilterType = 'all' | 'processed' | 'missingDrive' | 'missingHash' | 'noThumbnail' | 'localOnly' | 'withErrors';

export function StatsGrid({
  items,
  isLoading,
  activeFilter,
  onFilter,
}: {
  items: MediaItem[];
  isLoading?: boolean;
  activeFilter?: FilterType;
  onFilter?: (filter: FilterType) => void;
}) {
  const processed = items.filter((item) => item.drive_link || item.download_link).length;
  const missingDrive = items.filter((item) => !item.drive_link && !item.download_link).length;
  const missingHash = items.filter((item) => !item.file_hash).length;
  const noThumbnail = items.filter((item) => !item.thumb_url).length;
  const localOnly = items.filter((item) => item.local_path && !item.drive_link).length;
  const withErrors = items.filter((item) => Boolean(item.error) || String(item.status || '').includes('failed')).length;
  const cards: { label: string; value: number; icon: React.ElementType; filter: FilterType }[] = [
    { label: 'Totale record', value: items.length, icon: Database, filter: 'all' },
    { label: 'Processati', value: processed, icon: CheckCircle2, filter: 'processed' },
    { label: 'Senza Drive', value: missingDrive, icon: Cloud, filter: 'missingDrive' },
    { label: 'Senza hash', value: missingHash, icon: AlertTriangle, filter: 'missingHash' },
    { label: 'Senza thumbnail', value: noThumbnail, icon: ImageOff, filter: 'noThumbnail' },
    { label: 'Solo locali', value: localOnly, icon: Server, filter: 'localOnly' },
    { label: 'Con errori', value: withErrors, icon: AlertTriangle, filter: 'withErrors' },
  ];
  return (
    <div className="grid gap-6 sm:grid-cols-2 xl:grid-cols-4">
      {cards.map((card) => {
        const isActive = activeFilter === card.filter;
        return (
          <button
            key={card.label}
            onClick={() => onFilter?.(card.filter)}
            className={`rounded-3xl border p-8 text-left shadow-sm transition hover:shadow-md ${
              isActive 
                ? 'border-blue-500 bg-blue-50 dark:bg-blue-900/20 dark:border-blue-700' 
                : 'border-zinc-200 bg-white dark:border-zinc-800 dark:bg-zinc-900'
            }`}
          >
            <div className="flex items-center justify-between">
              <span className={`text-sm font-medium ${isActive ? 'text-blue-700 dark:text-blue-400' : 'text-zinc-500 dark:text-zinc-400'}`}>{card.label}</span>
              <card.icon className={`h-6 w-6 ${isActive ? 'text-blue-500' : 'text-zinc-400 dark:text-zinc-500'}`} />
            </div>
            <div className={`mt-4 text-4xl font-extrabold tracking-tight ${isActive ? 'text-blue-900 dark:text-blue-100' : 'text-zinc-900 dark:text-zinc-100'}`}>
              {isLoading ? (
                <Loader2 className="h-8 w-8 animate-spin text-zinc-300 dark:text-zinc-700" />
              ) : (
                card.value
              )}
            </div>
          </button>
        );
      })}
    </div>
  );
}
