import { AlertTriangle, X } from 'lucide-react';
import { useEffect, useState } from 'react';
import type { ClipPayload, MediaItem } from '../lib/types';
import { safeJson, withToken } from '../lib/utils';
import { Button } from './ui/Button';
import { findDuplicates } from '../api/media';

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
  const [duplicates, setDuplicates] = useState<Array<{
    source: string;
    id: string;
    name: string;
    drive_link: string;
    local_path: string;
    thumb_url: string;
  }>>([]);

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
      metadata: safeJson(item.metadata, {}),
      status: item.status,
      error: item.error,
    });
  }, [item]);

  useEffect(() => {
    if (!item) {
      setDuplicates([]);
      return;
    }
    findDuplicates(item.source as any, item.id)
      .then((data) => {
        if (data.ok && data.duplicates) {
          setDuplicates(data.duplicates);
        }
      })
      .catch(() => setDuplicates([]));
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
      <aside className="absolute right-0 top-0 h-full w-full max-w-[720px] overflow-y-auto border-l border-zinc-200 bg-white shadow-2xl dark:border-zinc-800 dark:bg-zinc-900">
        <div className="sticky top-0 z-10 flex h-16 items-center justify-between border-b border-zinc-200 bg-white/95 px-5 backdrop-blur dark:border-zinc-800 dark:bg-zinc-900/95">
          <div>
            <h2 className="text-base font-bold text-zinc-900 dark:text-zinc-50">Modifica asset</h2>
            <p className="text-xs text-zinc-500 dark:text-zinc-400">{item.source} • {item.id}</p>
          </div>
          <Button variant="ghost" onClick={onClose}><X className="h-4 w-4" /></Button>
        </div>

        <div className="grid gap-4 p-5">
          <Preview item={item} />

          {duplicates.length > 0 && (
            <div className="rounded-2xl border border-amber-200 bg-amber-50 p-4 text-sm text-amber-800">
              <div className="flex items-center gap-2 font-semibold">
                <AlertTriangle className="h-4 w-4" />
                Questo asset ha {duplicates.length} duplicati con lo stesso file hash.
              </div>
              <ul className="mt-2 space-y-1">
                {duplicates.map((dup) => (
                  <li key={`${dup.source}-${dup.id}`} className="text-xs">
                    {dup.source} • {dup.name}
                  </li>
                ))}
              </ul>
            </div>
          )}

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
          <label className="grid gap-1.5 text-sm font-semibold text-zinc-700 dark:text-zinc-300">
            Metadata JSON
            <textarea value={String(draft.metadata ?? '')} onChange={(event) => update('metadata', event.target.value)} className="min-h-40 rounded-xl border border-zinc-200 bg-zinc-50 px-3 py-2 font-mono text-xs outline-none transition focus:border-blue-500 focus:bg-white focus:ring-4 focus:ring-blue-500/10 dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-300 dark:focus:border-blue-500 dark:focus:bg-zinc-900" />
          </label>
          <div className="flex justify-between items-center border-t border-zinc-200 pt-4 dark:border-zinc-800">
            <div className="flex gap-2">
                <Button 
                  variant="secondary" 
                  onClick={() => {
                    navigator.clipboard.writeText(item.local_path || '');
                    alert('Path copiato negli appunti');
                  }}
                  title="Copia path locale"
                >
                  <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" className="mr-2"><rect x="9" y="9" width="13" height="13" rx="2" ry="2"></rect><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"></path></svg>
                  Copia Path
                </Button>
                <Button 
                  variant="secondary" 
                  onClick={() => {
                    const url = withToken(`/api/media/${item.source}/clips/${item.id}/download`);
                    const a = document.createElement('a');
                    a.href = url;
                    a.download = item.filename || item.name || 'download';
                    document.body.appendChild(a);
                    a.click();
                    document.body.removeChild(a);
                  }}
                  title="Scarica file originale"
                >
                  <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" className="mr-2"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"></path><polyline points="7 10 12 15 17 10"></polyline><line x1="12" y1="15" x2="12" y2="3"></line></svg>
                  Scarica
                </Button>
            </div>
            <div className="flex gap-2">
              <Button variant="secondary" onClick={onClose}>Annulla</Button>
              <Button onClick={() => onSave(draft)}>Salva modifiche</Button>
            </div>
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
    item.source === "stock" ||
    filename.endsWith(".mp4") ||
    filename.endsWith(".mov") ||
    filename.endsWith(".webm") ||
    filename.endsWith(".mkv") ||
    filename.endsWith(".avi") ||
    filename.endsWith(".m4v")
  );
}

function isAudioAsset(item: MediaItem): boolean {
  const filename = String(
    item.filename || item.local_path || item.download_link || item.drive_link || ""
  ).toLowerCase();

  return (
    filename.endsWith(".mp3") ||
    filename.endsWith(".wav") ||
    filename.endsWith(".m4a") ||
    filename.endsWith(".ogg") ||
    filename.endsWith(".aac")
  );
}

function isTextureAsset(item: MediaItem): boolean {
  const filename = String(
    item.filename || item.local_path || item.download_link || item.drive_link || ""
  ).toLowerCase();

  return filename.endsWith(".txt") || filename.endsWith(".json") || filename.endsWith(".md");
}

function AudioPreview({ item }: { item: MediaItem }) {
  const driveFileId = getDriveFileId(item);
  const audioSrc = (item.local_path || item.drive_file_id || driveFileId) 
    ? `/api/media/${item.source}/clips/${item.id}/download`
    : (item.preview_url || item.download_link || '');

  return (
    <div className="flex flex-col items-center justify-center space-y-4 overflow-hidden rounded-3xl border border-zinc-200 bg-zinc-50 p-8 shadow-inner dark:border-zinc-800 dark:bg-zinc-950">
      <div className="flex h-20 w-20 items-center justify-center rounded-full bg-blue-500 text-white shadow-lg shadow-blue-500/20">
        <svg xmlns="http://www.w3.org/2000/svg" width="40" height="40" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M9 18V5l12-2v13"></path><circle cx="6" cy="18" r="3"></circle><circle cx="18" cy="16" r="3"></circle></svg>
      </div>
      <audio
        controls
        src={withToken(audioSrc)}
        className="w-full"
      />
      <p className="text-xs font-medium text-zinc-400 dark:text-zinc-500">Riproduzione audio • {item.source}</p>
    </div>
  );
}

function TextPreview({ item }: { item: MediaItem }) {
  const [content, setContent] = useState<string>('');
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    const url = withToken(`/api/media/${item.source}/clips/${item.id}/download`);
    fetch(url)
      .then(res => res.text())
      .then(text => {
        setContent(text);
        setLoading(false);
      })
      .catch(() => {
        setContent('Errore nel caricamento del file di testo.');
        setLoading(false);
      });
  }, [item]);

  return (
    <div className="overflow-hidden rounded-3xl border border-zinc-200 bg-zinc-50 shadow-inner p-5 min-h-[200px] max-h-[500px] overflow-y-auto dark:border-zinc-800 dark:bg-zinc-950">
      <div className="text-[10px] font-black text-zinc-400 mb-3 uppercase tracking-widest border-b border-zinc-200 pb-1 dark:text-zinc-500 dark:border-zinc-800">Anteprima Testo</div>
      {loading ? (
        <div className="animate-pulse text-zinc-400 font-mono text-sm dark:text-zinc-600">Caricamento in corso...</div>
      ) : (
        <pre className="whitespace-pre-wrap font-mono text-sm text-zinc-800 leading-relaxed dark:text-zinc-300">{content}</pre>
      )}
    </div>
  );
}

function VideoPreview({ item }: { item: MediaItem }) {
  const driveFileId = getDriveFileId(item);
  const [error, setError] = useState(false);

  // 1. Try our smart proxy first (Local or Drive via Backend)
  // We use this if we have a way to identify the file
  if (!error && (item.local_path || item.drive_file_id || driveFileId)) {
    const videoSrc = `/api/media/${item.source}/clips/${item.id}/download`;
    return (
      <div className="overflow-hidden rounded-3xl border border-zinc-200 bg-zinc-950 shadow-inner dark:border-zinc-800">
        <video
          controls
          autoPlay
          muted
          preload="metadata"
          poster={item.thumb_url ? withToken(item.thumb_url) : undefined}
          src={withToken(videoSrc)}
          onError={() => setError(true)}
          className="max-h-[420px] w-full bg-zinc-950"
        />
      </div>
    );
  }

  // 2. Fallback to Drive iframe (more compatible but requires GDrive login in some cases)
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

  // 3. Last resort: direct links (like Artlist public URLs)
  return (
    <div className="overflow-hidden rounded-3xl border border-zinc-200 bg-zinc-950 shadow-inner">
      <video
        controls
        preload="metadata"
        poster={item.thumb_url ? withToken(item.thumb_url) : undefined}
        src={withToken(item.preview_url || item.download_link || '')}
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

    // If it's an image from Drive, use the thumbnail API
    if (item.source === 'images' && item.drive_file_id) {
      setImgSrc(`https://drive.google.com/thumbnail?id=${item.drive_file_id}&sz=w800-h600`);
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
    <div className="overflow-hidden rounded-3xl border border-zinc-200 bg-zinc-100 shadow-inner dark:border-zinc-800 dark:bg-zinc-950">
      <img
        src={withToken(imgSrc)}
        onError={() => setImgSrc(`https://placehold.co/800x600/ef4444/white?text=Preview+non+disponibile`)}
        className="max-h-[400px] w-full object-contain bg-zinc-50 dark:bg-zinc-950"
        alt="Preview"
      />
    </div>
  );
}

function Preview({ item }: { item: MediaItem }) {
  const isVideo = isVideoAsset(item);
  const isAudio = isAudioAsset(item);
  const isText = isTextureAsset(item);

  if (isVideo) {
    return <VideoPreview item={item} />;
  }

  if (isAudio) {
    return <AudioPreview item={item} />;
  }

  if (isText) {
    return <TextPreview item={item} />;
  }

  return <ImagePreview item={item} />;
}

function Field({ label, value, onChange }: { label: string; value: string; onChange: (value: string) => void }) {
  return (
    <label className="grid gap-1.5 text-sm font-semibold text-zinc-700 dark:text-zinc-300">
      {label}
      <input value={value} onChange={(event) => onChange(event.target.value)} className="h-10 rounded-xl border border-zinc-200 bg-zinc-50 px-3 text-sm font-normal outline-none transition focus:border-blue-500 focus:bg-white focus:ring-4 focus:ring-blue-500/10 dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-300 dark:focus:border-blue-500 dark:focus:bg-zinc-900" />
    </label>
  );
}
