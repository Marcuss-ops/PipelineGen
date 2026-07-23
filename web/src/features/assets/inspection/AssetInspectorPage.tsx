import { useCallback, useEffect, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import RefreshButton from '../../../components/RefreshButton'
import styles from './AssetInspector.module.css'
import { AssetHeader } from './components/AssetHeader'
import { AssetTabs } from './components/AssetTabs'
import { GeneralTab } from './components/tabs/GeneralTab'
import { PipelineTab } from './components/tabs/PipelineTab'
import { IndexingTab } from './components/tabs/IndexingTab'
import { StorageTab } from './components/tabs/StorageTab'
import { EventsTab } from './components/tabs/EventsTab'
import { useAssetInspection } from './hooks/useAssetInspection'
import { useAssetEditor } from './hooks/useAssetEditor'
import { TabKey } from './types'

export default function AssetInspectorPage() {
  const { id } = useParams<{ id: string }>()
  const [activeTab, setActiveTab] = useState<TabKey>('panoramica')
  const [saveMsg, setSaveMsg] = useState<{ type: 'ok' | 'err'; text: string } | null>(null)

  const {
    asset,
    loading,
    error,
    refresh,
    verifyResult,
    verifyLoading,
    reindexLoading,
    handleVerify,
    handleReindex,
    resetVerifyResult,
  } = useAssetInspection(id)

  const { form, dirty, saving, updateForm, handleSave } = useAssetEditor(id, asset, () => {
    setSaveMsg({ type: 'ok', text: 'Modifiche salvate e reindicizzazione richiesta.' })
    refresh()
  })

  useEffect(() => {
    resetVerifyResult()
  }, [id, resetVerifyResult])

  const handleVerifyWithMessage = useCallback(async () => {
    setSaveMsg(null)
    try {
      const res = await handleVerify()
      setSaveMsg({
        type: 'ok',
        text: `Verifica Qdrant completata: ${res.consistent ? 'coerente' : 'non coerente'}`,
      })
    } catch (err) {
      setSaveMsg({ type: 'err', text: err instanceof Error ? err.message : 'Errore verifica Qdrant' })
    }
  }, [handleVerify])

  const handleReindexWithMessage = useCallback(async () => {
    setSaveMsg(null)
    try {
      const res = await handleReindex()
      if (res.queued) {
        setSaveMsg({
          type: 'ok',
          text: 'Reindicizzazione accodata; lo stato si aggiornerà a breve.',
        })
      }
    } catch (err) {
      setSaveMsg({ type: 'err', text: err instanceof Error ? err.message : 'Errore reindicizzazione' })
    }
  }, [handleReindex])

  const handleSaveInternal = useCallback(async () => {
    setSaveMsg(null)
    try {
      await handleSave()
    } catch (err) {
      setSaveMsg({ type: 'err', text: err instanceof Error ? err.message : 'Errore di salvataggio' })
    }
  }, [handleSave])

  if (loading && !asset) {
    return <div className={styles.loadingText}>Caricamento asset...</div>
  }

  if (error || !asset) {
    return (
      <div className={styles.page}>
        <div className={styles.errorBox}>{error || 'Asset non trovato'}</div>
        <Link to="/content" className={styles.backLink}>
          ← Torna alla Content Library
        </Link>
      </div>
    )
  }

  return (
    <div className={styles.page}>
      <div className={styles.topBar}>
        <Link to="/content" className={styles.backLink}>
          ← Torna alla Content Library
        </Link>
        <RefreshButton onClick={refresh} />
      </div>

      <AssetHeader asset={asset} />

      <AssetTabs activeTab={activeTab} onTabChange={setActiveTab} />

      <div className={styles.tabPanel}>
        {activeTab === 'panoramica' && <GeneralTab asset={asset} />}
        {activeTab === 'pipeline' && <PipelineTab asset={asset} />}
        {activeTab === 'indicizzazione' && (
          <IndexingTab
            asset={asset}
            onVerify={handleVerifyWithMessage}
            onReindex={handleReindexWithMessage}
            verifyResult={verifyResult}
            verifyLoading={verifyLoading}
            reindexLoading={reindexLoading}
          />
        )}
        {activeTab === 'storage' && <StorageTab asset={asset} />}
        {activeTab === 'eventi' && <EventsTab asset={asset} />}
      </div>

      {saveMsg && (
        <div className={saveMsg.type === 'ok' ? styles.messageOk : styles.messageErr}>
          {saveMsg.text}
        </div>
      )}

      <div className={styles.footerActions}>
        <button onClick={handleSaveInternal} disabled={!dirty || saving} className={styles.primaryButton}>
          {saving ? 'Salvataggio...' : 'Salva modifiche'}
        </button>
        <button onClick={refresh} className={styles.secondaryButton}>
          Ricarica
        </button>
      </div>
    </div>
  )
}
