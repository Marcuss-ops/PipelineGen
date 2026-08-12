import { useEffect, useState } from 'react'
import { useParams, Link } from 'react-router-dom'
import { getScriptJobFull, ScriptJobFull, ScriptScene, ScriptDocument } from '../api/scripts'
import {
  ClipsPanel,
  DocumentsPanel,
  JsonPanel,
  ScenesPanel,
  ScriptBadge,
  TextPanel,
  tabButtonStyle,
} from './ScriptPanels'

export default function Script() {
  const { id } = useParams<{ id: string }>()
  const [script, setScript] = useState<ScriptJobFull | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [activeTab, setActiveTab] = useState<'text' | 'scenes' | 'clips' | 'translations' | 'voiceover' | 'docs' | 'metadata' | 'raw'>('text')

  useEffect(() => {
    if (!id) return
    setLoading(true)
    setError(null)
    getScriptJobFull(id)
      .then(setScript)
      .catch((err) => setError(err instanceof Error ? err.message : 'Errore caricamento script'))
      .finally(() => setLoading(false))
  }, [id])

  if (loading) return <div style={{ padding: '2rem', color: '#94a3b8', textAlign: 'center' }}>Caricamento script...</div>
  if (error || !script) return <div style={{ padding: '2rem' }}><div style={{ background: 'rgba(248,113,113,0.1)', border: '1px solid #f87171', color: '#f87171', padding: '1rem', borderRadius: '8px', marginBottom: '1rem' }}>{error || 'Script non trovato'}</div><Link to="/jobs" style={{ color: '#38bdf8', textDecoration: 'none' }}>← Torna ai Jobs</Link></div>

  const result = (script.result || {}) as Record<string, unknown>
  const fullText = typeof result.text === 'string' ? result.text : (result.script as string) || ''
  const sourceType = (result.source_type as string) || (result.sourceType as string) || '-'
  const model = (result.model as string) || script.current_stage || '-'
  const languages = Array.isArray(result.languages) ? (result.languages as string[]) : typeof result.languages === 'string' ? [result.languages as string] : []
  const clips = Array.isArray(result.clips) ? (result.clips as unknown[]) : []
  const scenes = script.scenes || (Array.isArray(result.scenes) ? (result.scenes as ScriptScene[]) : [])
  const documents = script.documents || (result.documents as Record<string, ScriptDocument>) || {}
  const voiceovers = result.voiceover || result.voiceovers
  const translations = result.translations || result.translated_text
  const metadata = result.metadata || ({} as Record<string, unknown>)
  const tabs = [
    { key: 'text', label: 'Testo' }, { key: 'scenes', label: 'Scene' }, { key: 'clips', label: 'Clip bindings' }, { key: 'translations', label: 'Traduzioni' },
    { key: 'voiceover', label: 'Voiceover' }, { key: 'docs', label: 'Google Doc' }, { key: 'metadata', label: 'Metadata' }, { key: 'raw', label: 'Raw JSON' },
  ] as { key: typeof activeTab; label: string }[]

  return (
    <div style={{ padding: '2rem' }}>
      <div style={{ marginBottom: '1.5rem' }}><Link to="/jobs" style={{ color: '#38bdf8', textDecoration: 'none', fontSize: '0.9rem' }}>← Torna ai Jobs</Link></div>
      <div style={{ background: '#1e293b', border: '1px solid #334155', borderRadius: '8px', padding: '1.5rem', marginBottom: '1.5rem' }}>
        <h2 style={{ margin: '0 0 1rem', fontSize: '1.75rem', color: '#e2e8f0' }}>Script {script.job_id.slice(0, 16)}...</h2>
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: '0.75rem' }}>
          <ScriptBadge label="Stato" value={script.status} /><ScriptBadge label="Stage" value={script.current_stage} /><ScriptBadge label="Source type" value={sourceType} /><ScriptBadge label="Scene" value={scenes.length.toString()} /><ScriptBadge label="Clip usate" value={clips.length.toString()} /><ScriptBadge label="Modello" value={model} /><ScriptBadge label="Word count" value={script.word_count !== undefined ? String(script.word_count) : undefined} />
          {languages.map((language) => <ScriptBadge key={language} label="Lingua" value={language} />)}
        </div>
        {script.error && <div style={{ marginTop: '1rem', background: 'rgba(248,113,113,0.1)', border: '1px solid #f87171', color: '#f87171', padding: '0.75rem', borderRadius: '6px' }}>{script.error}</div>}
      </div>
      <div style={{ display: 'flex', gap: '0.5rem', borderBottom: '1px solid #334155', marginBottom: '1.5rem', flexWrap: 'wrap' }}>
        {tabs.map((tab) => <button key={tab.key} onClick={() => setActiveTab(tab.key)} style={{ ...tabButtonStyle, borderBottom: activeTab === tab.key ? '2px solid #38bdf8' : '2px solid transparent', background: activeTab === tab.key ? '#0f172a' : 'transparent', color: activeTab === tab.key ? '#38bdf8' : '#94a3b8' }}>{tab.label}</button>)}
      </div>
      {activeTab === 'text' && <TextPanel text={fullText} />}
      {activeTab === 'scenes' && <ScenesPanel scenes={scenes} />}
      {activeTab === 'clips' && <ClipsPanel clips={clips} />}
      {activeTab === 'translations' && <JsonPanel title="Traduzioni" value={translations} emptyMessage="Nessuna traduzione disponibile." />}
      {activeTab === 'voiceover' && <JsonPanel title="Voiceover" value={voiceovers} emptyMessage="Nessun voiceover disponibile." />}
      {activeTab === 'docs' && <DocumentsPanel documents={documents} />}
      {activeTab === 'metadata' && <JsonPanel title="Metadata" value={metadata} emptyMessage="Nessun metadata disponibile." emptyWhenEmptyObject />}
      {activeTab === 'raw' && <JsonPanel title="Raw JSON" value={script} emptyMessage="Dati non disponibili." />}
    </div>
  )
}
