import { X } from 'lucide-react';
import { useEffect, useState } from 'react';
import type { ClipPayload, MediaItem } from '../lib/types';
import { safeJson } from '../lib/utils';
import { Button } from './ui/Button';

export function MediaDetailDrawer({
  item,
  open,
  onClose,
  onSave,
}: {
  item: MediaItem | null;
  open: boolean;
  onClose: () => void;
  onSave: (payload: ClipPayload) => void;
}) {
  const [draft, setDraft] = useState<ClipPayload>({});

  useEffect(() => {
    if (!item) return;
    setDraft({
      name: item.name,
      filename: item.filename,
      category: item.category,
      tags: item.tags,
      search_terms: item.search_terms,
      external_url: item.external_url,
      drive_link: item.drive_link,
      drive_file_id: item.drive_file_id,
      download_link: item.download_link,
      local_path: item.local_path,
      file_hash: item.file_hash,
      folder_id: item.folder_id,
      folder_path: item.folder_path,
      duration: item.duration,
      metadata: safeJson(item.metadata),
      status: item.status,
      error: item.error,
    });
  }, [item]);

  if (!open || !item) return null;

  const update = (key: keyof ClipPayload, value: string) => {
    if (key === 'tags' || key === 'search_terms') {
      setDraft((previous) => ({ ...previous, [key]: value.split(',').map((x) => x.trim()).filter(Boolean) }));
      return;
    }
    setDraft((previous) => ({ ...previous, [key]: value }));
  };

  return (
    <div className="fixed inset-0 z-50">
      <button className="absolute inset-0 bg-zinc-950/30 backdrop-blur-sm" onClick={onClose} aria-label="Chiudi" />
      <aside className="absolute right-0 top-0 h-full w-full max-w-[720px] overflow-y-auto border-l border-zinc-200 bg-white shadow-2xl">
        <div className="sticky top-0 z-10 flex h-16 items-center justify-between border-b border-zinc-200 bg-white/95 px-5 backdrop-blur">
          <div>
            <h2 className="text-base font-bold">Modifica asset</h2>
            <p className="text-xs text-zinc-500">{item.source} • {item.id}</p>
          </div>
          <Button variant="ghost" onClick={onClose}><X className="h-4 w-4" /></Button>
        </div>

        <div className="grid gap-4 p-5">
          <Preview item={item} />
          <Field label="Nome" value={String(draft.name ?? '')} onChange={(v) => update('name', v)} />
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="Filename" value={String(draft.filename ?? '')} onChange={(v) => update('filename', v)} />
            <Field label="Categoria" value={String(draft.category ?? '')} onChange={(v) => update('category', v)} />
          </div>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="Tags, separati da virgola" value={(draft.tags as string[] | undefined)?.join(', ') ?? ''} onChange={(v) => update('tags', v)} />
            <Field label="Search terms" value={(draft.search_terms as string[] | undefined)?.join(', ') ?? ''} onChange={(v) => update('search_terms', v)} />
          </div>
          <Field label="External URL" value={String(draft.external_url ?? '')} onChange={(v) => update('external_url', v)} />
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="Drive link" value={String(draft.drive_link ?? '')} onChange={(v) => update('drive_link', v)} />
            <Field label="Drive file ID" value={String(draft.drive_file_id ?? '')} onChange={(v) => update('drive_file_id', v)} />
          </div>
          <Field label="Download link" value={String(draft.download_link ?? '')} onChange={(v) => update('download_link', v)} />
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="Local path" value={String(draft.local_path ?? '')} onChange={(v) => update('local_path', v)} />
            <Field label="File hash" value={String(draft.file_hash ?? '')} onChange={(v) => update('file_hash', v)} />
          </div>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="Folder ID" value={String(draft.folder_id ?? '')} onChange={(v) => update('folder_id', v)} />
            <Field label="Folder path" value={String(draft.folder_path ?? '')} onChange={(v) => update('folder_path', v)} />
          </div>
          <label className="grid gap-1.5 text-sm font-semibold text-zinc-700">
            Metadata JSON
            <textarea value={String(draft.metadata ?? '')} onChange={(event) => update('metadata', event.target.value)} className="min-h-40 rounded-xl border border-zinc-200 bg-zinc-50 px-3 py-2 font-mono text-xs outline-none transition focus:border-blue-500 focus:bg-white focus:ring-4 focus:ring-blue-500/10" />
          </label>
          <div className="flex justify-end gap-2 border-t border-zinc-200 pt-4">
            <Button variant="secondary" onClick={onClose}>Annulla</Button>
            <Button onClick={() => onSave(draft)}>Salva modifiche</Button>
          </div>
        </div>
      </aside>
    </div>
  );
}

function getDriveFileId(item: MediaItem): string {
  if (item.drive_file_id) return item.drive_file_id;

  const value = item.drive_link || item.download_link || "";

  const fileMatch = value.match(/\/file\/d\/([^/?]+)/);
  if (fileMatch?.[1]) return fileMatch[1];

  const idMatch = value.match(/[?&]id=([^&]+)/);
  if (idMatch?.[1]) return idMatch[1];

  return "";
}

function isVideoAsset(item: MediaItem): boolean {
  const filename = String(
    item.filename || item.local_path || item.download_link || item.drive_link || ""
  ).toLowerCase();

  return (
    item.source === "artlist" ||
    item.source === "youtube" ||
    item.source === "clips" ||
    item.source === "stock" ||
    filename.endsWith(".mp4") ||
    filename.endsWith(".mov") ||
    filename.endsWith(".webm") ||
    filename.endsWith(".mkv") ||
    filename.endsWith(".avi") ||
    filename.endsWith(".m4v")
  );
}

function VideoPreview({ item }: { item: MediaItem }) {
  const driveFileId = getDriveFileId(item);

  if (driveFileId) {
    return (
      <div className="overflow-hidden rounded-3xl border border-zinc-200 bg-zinc-950 shadow-inner">
        <iframe
          src={`https://drive.google.com/file/d/${driveFileId}/preview`}
          allow="autoplay; fullscreen"
          allowFullScreen
          className="aspect-video w-full bg-zinc-950"
          title={item.name || "Drive video preview"}
        />
      </div>
    );
  }

  return (
    <div className="overflow-hidden rounded-3xl border border-zinc-200 bg-zinc-950 shadow-inner">
      <video
        controls
        preload="metadata"
        poster={item.thumb_url || undefined}
        src={item.preview_url || item.download_link}
        className="max-h-[420px] w-full bg-zinc-950"
      />
    </div>
  );
}

function ImagePreview({ item }: { item: MediaItem }) {
  const [imgSrc, setImgSrc] = useState<string>(
    item.preview_url || item.thumb_url || ''
  );

  useEffect(() => {
    if (item.preview_url || item.thumb_url) {
      setImgSrc(item.preview_url || item.thumb_url || '');
      return;
    }
    if (item.drive_link) {
      const match = item.drive_link.match(/\/d\/([^/?]+)/);
      if (match) {
        setImgSrc(`https://drive.google.com/thumbnail?id=${match[1]}&sz=w800-h600`);
        return;
      }
    }
    setImgSrc(`https://placehold.co/800x600/f8fafc/64748b?text=${encodeURIComponent(item.name || item.source)}`);
  }, [item]);

  return (
    <div className="overflow-hidden rounded-3xl border border-zinc-200 bg-zinc-100 shadow-inner">
      <img
        src={imgSrc}
        onError={() => setImgSrc(`https://placehold.co/800x600/ef4444/white?text=Preview+non+disponibile`)}
        className="max-h-[400px] w-full object-contain bg-zinc-50"
        alt="Preview"
      />
    </div>
  );
}

function Preview({ item }: { item: MediaItem }) {
  const isVideo = isVideoAsset(item);

  if (isVideo) {
    return <VideoPreview item={item} />;
  }

  return <ImagePreview item={item} />;
}

function Field({ label, value, onChange }: { label: string; value: string; onChange: (value: string) => void }) {
  return (
    <label className="grid gap-1.5 text-sm font-semibold text-zinc-700">
      {label}
      <input value={value} onChange={(event) => onChange(event.target.value)} className="h-10 rounded-xl border border-zinc-200 bg-zinc-50 px-3 text-sm font-normal outline-none transition focus:border-blue-500 focus:bg-white focus:ring-4 focus:ring-blue-500/10" />
    </label>
  );
}
