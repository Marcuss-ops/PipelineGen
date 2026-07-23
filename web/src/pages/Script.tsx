import { useEffect, useState } from 'react'
import { useParams, Link } from 'react-router-dom'
import { getScriptJobFull, ScriptJobFull, ScriptScene } from '../api/scripts'

export default function Script() {
  const { id } = useParams<{ id: string }>()
  const [script, setScript] = useState<ScriptJobFull | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [activeTab, setActiveTab] = useState<
    'text' | 'scenes' | 'clips' | 'translations' | 'voiceover' | 'docs' | 'metadata' | 'raw'
  >('text')

  useEffect(() => {
    if (!id) return
    setLoading(true)
    setError(null)
    getScriptJobFull(id)
      .then(setScript)
      .catch((err) => setError(err instanceof Error ? err.message : 'Errore caricamento script'))
      .finally(() => setLoading(false))
  }, [id])

  if (loading) {
    return <div style={{ padding: '2rem', color: '#94a3b8', textAlign: 'center' }}>Caricamento script...</div>
  }

  if (error || !script) {
    return (
      <div style={{ padding: '2rem' }}>
        <div
          style={{
            background: 'rgba(248,113,113,0.1)',
            border: '1px solid #f87171',
            color: '#f87171',
            padding: '1rem',
            borderRadius: '8px',
            marginBottom: '1rem',
          }}
        >
          {error || 'Script non trovato'}
        </div>
        <Link to="/jobs" style={{ color: '#38bdf8', textDecoration: 'none' }}>
          ← Torna ai Jobs
        </Link>
      </div>
    )
  }

  const result = (script.result || {}) as Record<string, unknown>
  const fullText = typeof result.text === 'string' ? result.text : (result.script as string) || ''
  const sourceType = (result.source_type as string) || (result.sourceType as string) || '-'
  const model = (result.model as string) || script.current_stage || '-'
  const languages = Array.isArray(result.languages)
    ? (result.languages as string[])
    : typeof result.languages === 'string'
    ? [result.languages as string]
    : []
  const clips = Array.isArray(result.clips) ? (result.clips as unknown[]) : []
  const scenes = script.scenes || (Array.isArray(result.scenes) ? (result.scenes as ScriptScene[]) : [])
  const documents = script.documents || (result.documents as Record<string, { id: string; link: string }>) || {}
  const voiceovers = result.voiceover || result.voiceovers
  const translations = result.translations || result.translated_text
  const metadata = result.metadata || ({} as Record<string, unknown>)

  return (
    <div style={{ padding: '2rem' }}>
      <div style={{ marginBottom: '1.5rem' }}>
        <Link to="/jobs" style={{ color: '#38bdf8', textDecoration: 'none', fontSize: '0.9rem' }}>
          ← Torna ai Jobs
        </Link>
      </div>

      <div
        style={{
          background: '#1e293b',
          border: '1px solid #334155',
          borderRadius: '8px',
          padding: '1.5rem',
          marginBottom: '1.5rem',
        }}
      >
        <h2 style={{ margin: '0 0 1rem', fontSize: '1.75rem', color: '#e2e8f0' }}>
          Script {script.job_id.slice(0, 16)}...
        </h2>
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: '0.75rem' }}>
          <Badge label="Stato" value={script.status} />
          <Badge label="Stage" value={script.current_stage} />
          <Badge label="Source type" value={sourceType} />
          <Badge label="Scene" value={scenes.length.toString()} />
          <Badge label="Clip usate" value={clips.length.toString()} />
          <Badge label="Modello" value={model} />
          <Badge label="Word count" value={script.word_count !== undefined ? String(script.word_count) : undefined} />
          {languages.map((lang) => (
            <Badge key={lang} label="Lingua" value={lang} />
          ))}
        </div>
        {script.error && (
          <div
            style={{
              marginTop: '1rem',
              background: 'rgba(248,113,113,0.1)',
              border: '1px solid #f87171',
              color: '#f87171',
              padding: '0.75rem',
              borderRadius: '6px',
            }}
          >
            {script.error}
          </div>
        )}
      </div>

      <div style={{ display: 'flex', gap: '0.5rem', borderBottom: '1px solid #334155', marginBottom: '1.5rem', flexWrap: 'wrap' }}>
        {(
          [
            { key: 'text', label: 'Testo' },
            { key: 'scenes', label: 'Scene' },
            { key: 'clips', label: 'Clip bindings' },
            { key: 'translations', label: 'Traduzioni' },
            { key: 'voiceover', label: 'Voiceover' },
            { key: 'docs', label: 'Google Doc' },
            { key: 'metadata', label: 'Metadata' },
            { key: 'raw', label: 'Raw JSON' },
          ] as { key: typeof activeTab; label: string }[]
        ).map((t) => (
          <button
            key={t.key}
            onClick={() => setActiveTab(t.key)}
            style={{
              ...tabButtonStyle,
              borderBottom: activeTab === t.key ? '2px solid #38bdf8' : '2px solid transparent',
              background: activeTab === t.key ? '#0f172a' : 'transparent',
              color: activeTab === t.key ? '#38bdf8' : '#94a3b8',
            }}
          >
            {t.label}
          </button>
        ))}
      </div>

      {activeTab === 'text' && (
        <Panel title="Testo completo">
          {fullText ? (
            <pre style={preStyle}>{fullText}</pre>
          ) : (
            <EmptyState>Testo non disponibile.</EmptyState>
          )}
        </Panel>
      )}

      {activeTab === 'scenes' && (
        <Panel title="Scene">
          {scenes.length > 0 ? (
            <div style={{ display: 'flex', flexDirection: 'column', gap: '0.75rem' }}>
              {scenes.map((scene) => (
                <div
                  key={scene.id}
                  style={{
                    background: '#1e293b',
                    border: '1px solid #334155',
                    borderRadius: '8px',
                    padding: '1rem',
                  }}
                >
                  <div style={{ color: '#38bdf8', fontWeight: 600, marginBottom: '0.5rem' }}>
                    Scene #{scene.index} <span style={{ color: '#64748b' }}>({scene.id})</span>
                  </div>
                  {Object.entries(scene.text || {}).map(([lang, text]) => (
                    <div key={lang} style={{ marginBottom: '0.5rem' }}>
                      <span style={{ color: '#94a3b8', fontSize: '0.8rem' }}>{lang}: </span>
                      <span style={{ color: '#e2e8f0' }}>{text}</span>
                    </div>
                  ))}
                  {scene.clip && <pre style={preStyle}>{JSON.stringify(scene.clip, null, 2)}</pre>}
                </div>
              ))}
            </div>
          ) : (
            <EmptyState>Nessuna scena disponibile.</EmptyState>
          )}
        </Panel>
      )}

      {activeTab === 'clips' && (
        <Panel title="Clip bindings">
          {clips.length > 0 ? (
            <div style={{ display: 'flex', flexDirection: 'column', gap: '0.75rem' }}>
              {clips.map((clip: any, idx: number) => (
                <div
                  key={idx}
                  style={{
                    background: '#1e293b',
                    border: '1px solid #334155',
                    borderRadius: '8px',
                    padding: '1rem',
                  }}
                >
                  <pre style={preStyle}>{JSON.stringify(clip, null, 2)}</pre>
                </div>
              ))}
            </div>
          ) : (
            <EmptyState>Nessuna clip associata.</EmptyState>
          )}
        </Panel>
      )}

      {activeTab === 'translations' && (
        <Panel title="Traduzioni">
          {translations ? (
            <pre style={preStyle}>{JSON.stringify(translations, null, 2)}</pre>
          ) : (
            <EmptyState>Nessuna traduzione disponibile.</EmptyState>
          )}
        </Panel>
      )}

      {activeTab === 'voiceover' && (
        <Panel title="Voiceover">
          {voiceovers ? (
            <pre style={preStyle}>{JSON.stringify(voiceovers, null, 2)}</pre>
          ) : (
            <EmptyState>Nessun voiceover disponibile.</EmptyState>
          )}
        </Panel>
      )}

      {activeTab === 'docs' && (
        <Panel title="Google Doc">
          {documents && Object.keys(documents).length > 0 ? (
            <div style={{ display: 'flex', flexDirection: 'column', gap: '0.75rem' }}>
              {Object.entries(documents).map(([lang, doc]) => (
                <div
                  key={lang}
                  style={{
                    background: '#1e293b',
                    border: '1px solid #334155',
                    borderRadius: '8px',
                    padding: '1rem',
                    display: 'flex',
                    justifyContent: 'space-between',
                    alignItems: 'center',
                  }}
                >
                  <div>
                    <div style={{ color: '#38bdf8', fontWeight: 600 }}>{lang}</div>
                    <div style={{ color: '#94a3b8', fontSize: '0.85rem' }}>{doc.id}</div>
                  </div>
                  <a
                    href={doc.link}
                    target="_blank"
                    rel="noopener noreferrer"
                    style={{ ...buttonStyle, background: '#1e293b', color: '#38bdf8' }}
                  >
                    Apri Doc
                  </a>
                </div>
              ))}
            </div>
          ) : (
            <EmptyState>Nessun documento disponibile.</EmptyState>
          )}
        </Panel>
      )}

      {activeTab === 'metadata' && (
        <Panel title="Metadata">
          {metadata && Object.keys(metadata).length > 0 ? (
            <pre style={preStyle}>{JSON.stringify(metadata, null, 2)}</pre>
          ) : (
            <EmptyState>Nessun metadata disponibile.</EmptyState>
          )}
        </Panel>
      )}

      {activeTab === 'raw' && (
        <Panel title="Raw JSON">
          <pre style={preStyle}>{JSON.stringify(script, null, 2)}</pre>
        </Panel>
      )}
    </div>
  )
}

function Badge({ label, value }: { label: string; value?: string }) {
  if (!value) return null
  return (
    <div
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: '0.35rem',
        background: '#0f172a',
        border: '1px solid #334155',
        borderRadius: '6px',
        padding: '0.35rem 0.65rem',
        fontSize: '0.8rem',
      }}
    >
      <span style={{ color: '#64748b' }}>{label}:</span>
      <span style={{ color: '#e2e8f0', fontWeight: 500 }}>{value}</span>
    </div>
  )
}

function Panel({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div>
      <h3 style={{ margin: '0 0 1rem', color: '#38bdf8' }}>{title}</h3>
      {children}
    </div>
  )
}

function EmptyState({ children }: { children: React.ReactNode }) {
  return <div style={{ color: '#94a3b8', textAlign: 'center', padding: '2rem' }}>{children}</div>
}

const buttonStyle: React.CSSProperties = {
  padding: '0.55rem 1rem',
  background: '#38bdf8',
  color: '#0f172a',
  border: 'none',
  borderRadius: '6px',
  fontWeight: 600,
  cursor: 'pointer',
  textDecoration: 'none',
  display: 'inline-flex',
  alignItems: 'center',
}

const tabButtonStyle: React.CSSProperties = {
  padding: '0.75rem 1rem',
  background: 'transparent',
  border: 'none',
  borderRadius: '6px 6px 0 0',
  cursor: 'pointer',
  fontSize: '0.9rem',
  fontWeight: 500,
}

const preStyle: React.CSSProperties = {
  background: '#0f172a',
  color: '#e2e8f0',
  padding: '1rem',
  borderRadius: '8px',
  fontSize: '0.75rem',
  overflowX: 'auto',
  whiteSpace: 'pre-wrap',
  wordBreak: 'break-all',
  maxHeight: '60vh',
  overflowY: 'auto',
  border: '1px solid #334155',
}
