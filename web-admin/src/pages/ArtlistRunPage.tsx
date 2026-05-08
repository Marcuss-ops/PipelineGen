import { useMutation } from '@tanstack/react-query';
import { Play, Terminal } from 'lucide-react';
import { useState } from 'react';
import { runArtlistPipeline, type ArtlistRunPayload } from '../api/artlist';
import { Button } from '../components/ui/Button';

export function ArtlistRunPage() {
  const [payload, setPayload] = useState<ArtlistRunPayload>({
    term: 'cinematic city',
    limit: 5,
    strategy: 'verify',
    dry_run: false,
    clip_duration: 7,
    width: 1920,
    height: 1080,
    fps: 30,
  });
  const [result, setResult] = useState<unknown>(null);

  const mutation = useMutation({
    mutationFn: runArtlistPipeline,
    onSuccess: setResult,
    onError: (error) => setResult({ ok: false, error: String(error) }),
  });

  const update = <K extends keyof ArtlistRunPayload>(key: K, value: ArtlistRunPayload[K]) => setPayload((prev) => ({ ...prev, [key]: value }));

  return (
    <div className="grid gap-5 xl:grid-cols-[480px_1fr]">
      <section className="rounded-3xl border border-zinc-200 bg-white p-5 shadow-sm">
        <p className="text-sm font-semibold text-orange-600">Artlist pipeline</p>
        <h2 className="mt-1 text-2xl font-extrabold tracking-tight">Lancia /api/artlist/run</h2>
        <p className="mt-2 text-sm leading-6 text-zinc-500">Usa questa pagina per testare download, processing, upload Drive e salvataggio DB senza usare curl.</p>
        <div className="mt-5 grid gap-4">
          <Field label="Termine" value={payload.term} onChange={(v) => update('term', v)} />
          <div className="grid grid-cols-2 gap-3">
            <Field label="Limit" type="number" value={String(payload.limit)} onChange={(v) => update('limit', Number(v))} />
            <label className="grid gap-1.5 text-sm font-semibold text-zinc-700">
              Strategy
              <select value={payload.strategy} onChange={(e) => update('strategy', e.target.value as ArtlistRunPayload['strategy'])} className="h-10 rounded-xl border border-zinc-200 bg-zinc-50 px-3 text-sm font-normal outline-none focus:border-orange-500 focus:ring-4 focus:ring-orange-500/10">
                <option value="verify">verify</option>
                <option value="skip">skip</option>
                <option value="replace">replace</option>
              </select>
            </label>
          </div>
          <Field label="Root folder ID" value={payload.root_folder_id ?? ''} onChange={(v) => update('root_folder_id', v)} />
          <div className="grid grid-cols-2 gap-3">
            <Field label="Durata" type="number" value={String(payload.clip_duration ?? '')} onChange={(v) => update('clip_duration', Number(v))} />
            <Field label="FPS" type="number" value={String(payload.fps ?? '')} onChange={(v) => update('fps', Number(v))} />
          </div>
          <div className="grid grid-cols-2 gap-3">
            <Field label="Width" type="number" value={String(payload.width ?? '')} onChange={(v) => update('width', Number(v))} />
            <Field label="Height" type="number" value={String(payload.height ?? '')} onChange={(v) => update('height', Number(v))} />
          </div>
          <label className="flex items-center gap-2 rounded-xl border border-zinc-200 bg-zinc-50 px-3 py-2 text-sm font-semibold">
            <input type="checkbox" checked={Boolean(payload.dry_run)} onChange={(e) => update('dry_run', e.target.checked)} /> Dry run
          </label>
          <Button onClick={() => mutation.mutate(payload)} disabled={mutation.isPending}>
            <Play className="h-4 w-4" /> {mutation.isPending ? 'Esecuzione...' : 'Run pipeline'}
          </Button>
        </div>
      </section>

      <section className="rounded-3xl border border-zinc-200 bg-zinc-950 p-5 text-white shadow-sm">
        <div className="flex items-center gap-2 text-sm font-semibold text-zinc-300"><Terminal className="h-4 w-4" /> Risultato</div>
        <pre className="mt-4 max-h-[680px] overflow-auto rounded-2xl bg-black/40 p-4 text-xs leading-6 text-zinc-200">{JSON.stringify(result ?? { status: 'Pronto' }, null, 2)}</pre>
      </section>
    </div>
  );
}

function Field({ label, value, onChange, type = 'text' }: { label: string; value: string; onChange: (value: string) => void; type?: string }) {
  return (
    <label className="grid gap-1.5 text-sm font-semibold text-zinc-700">
      {label}
      <input type={type} value={value} onChange={(event) => onChange(event.target.value)} className="h-10 rounded-xl border border-zinc-200 bg-zinc-50 px-3 text-sm font-normal outline-none transition focus:border-orange-500 focus:bg-white focus:ring-4 focus:ring-orange-500/10" />
    </label>
  );
}
