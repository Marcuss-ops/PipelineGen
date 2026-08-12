import type { CSSProperties, ReactNode } from 'react'
import type { ScriptDocument, ScriptScene } from '../api/scripts'

export function ScriptBadge({ label, value }: { label: string; value?: string }) {
  if (!value) return null
  return <div style={{ display: 'flex', alignItems: 'center', gap: '0.35rem', background: '#0f172a', border: '1px solid #334155', borderRadius: '6px', padding: '0.35rem 0.65rem', fontSize: '0.8rem' }}><span style={{ color: '#64748b' }}>{label}:</span><span style={{ color: '#e2e8f0', fontWeight: 500 }}>{value}</span></div>
}

export function ScriptPanel({ title, children }: { title: string; children: ReactNode }) {
  return <div><h3 style={{ margin: '0 0 1rem', color: '#38bdf8' }}>{title}</h3>{children}</div>
}

export function ScriptEmptyState({ children }: { children: ReactNode }) {
  return <div style={{ color: '#94a3b8', textAlign: 'center', padding: '2rem' }}>{children}</div>
}

export function TextPanel({ text }: { text: string }) {
  return <ScriptPanel title="Testo completo">{text ? <pre style={preStyle}>{text}</pre> : <ScriptEmptyState>Testo non disponibile.</ScriptEmptyState>}</ScriptPanel>
}

export function ScenesPanel({ scenes }: { scenes: ScriptScene[] }) {
  return <ScriptPanel title="Scene">{scenes.length > 0 ? <div style={{ display: 'flex', flexDirection: 'column', gap: '0.75rem' }}>{scenes.map((scene) => <div key={scene.id} style={{ background: '#1e293b', border: '1px solid #334155', borderRadius: '8px', padding: '1rem' }}><div style={{ color: '#38bdf8', fontWeight: 600, marginBottom: '0.5rem' }}>Scene #{scene.index} <span style={{ color: '#64748b' }}>({scene.id})</span></div>{Object.entries(scene.text || {}).map(([lang, text]) => <div key={lang} style={{ marginBottom: '0.5rem' }}><span style={{ color: '#94a3b8', fontSize: '0.8rem' }}>{lang}: </span><span style={{ color: '#e2e8f0' }}>{text}</span></div>)}{scene.clip && <pre style={preStyle}>{JSON.stringify(scene.clip, null, 2)}</pre>}</div>)}</div> : <ScriptEmptyState>Nessuna scena disponibile.</ScriptEmptyState>}</ScriptPanel>
}

export function ClipsPanel({ clips }: { clips: unknown[] }) {
  return <ScriptPanel title="Clip bindings">{clips.length > 0 ? <div style={{ display: 'flex', flexDirection: 'column', gap: '0.75rem' }}>{clips.map((clip, idx) => <div key={idx} style={{ background: '#1e293b', border: '1px solid #334155', borderRadius: '8px', padding: '1rem' }}><pre style={preStyle}>{JSON.stringify(clip, null, 2)}</pre></div>)}</div> : <ScriptEmptyState>Nessuna clip associata.</ScriptEmptyState>}</ScriptPanel>
}

export function JsonPanel({ title, value, emptyMessage, emptyWhenEmptyObject = false }: { title: string; value: unknown; emptyMessage: string; emptyWhenEmptyObject?: boolean }) {
  const isEmptyObject = emptyWhenEmptyObject && typeof value === 'object' && value !== null && Object.keys(value).length === 0
  return <ScriptPanel title={title}>{value && !isEmptyObject ? <pre style={preStyle}>{JSON.stringify(value, null, 2)}</pre> : <ScriptEmptyState>{emptyMessage}</ScriptEmptyState>}</ScriptPanel>
}

export function DocumentsPanel({ documents }: { documents: Record<string, ScriptDocument> }) {
  return <ScriptPanel title="Google Doc">{documents && Object.keys(documents).length > 0 ? <div style={{ display: 'flex', flexDirection: 'column', gap: '0.75rem' }}>{Object.entries(documents).map(([lang, doc]) => <div key={lang} style={{ background: '#1e293b', border: '1px solid #334155', borderRadius: '8px', padding: '1rem', display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}><div><div style={{ color: '#38bdf8', fontWeight: 600 }}>{lang}</div><div style={{ color: '#94a3b8', fontSize: '0.85rem' }}>{doc.id}</div></div><a href={doc.link} target="_blank" rel="noopener noreferrer" style={{ ...buttonStyle, background: '#1e293b', color: '#38bdf8' }}>Apri Doc</a></div>)}</div> : <ScriptEmptyState>Nessun documento disponibile.</ScriptEmptyState>}</ScriptPanel>
}

export const buttonStyle: CSSProperties = { padding: '0.55rem 1rem', background: '#38bdf8', color: '#0f172a', border: 'none', borderRadius: '6px', fontWeight: 600, cursor: 'pointer', textDecoration: 'none', display: 'inline-flex', alignItems: 'center' }

export const tabButtonStyle: CSSProperties = { padding: '0.75rem 1rem', background: 'transparent', border: 'none', borderRadius: '6px 6px 0 0', cursor: 'pointer', fontSize: '0.9rem', fontWeight: 500 }

const preStyle: CSSProperties = { background: '#0f172a', color: '#e2e8f0', padding: '1rem', borderRadius: '8px', fontSize: '0.75rem', overflowX: 'auto', whiteSpace: 'pre-wrap', wordBreak: 'break-all', maxHeight: '60vh', overflowY: 'auto', border: '1px solid #334155' }
