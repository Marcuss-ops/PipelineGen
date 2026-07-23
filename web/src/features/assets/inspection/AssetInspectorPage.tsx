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
import { ProcessingTab } from './components/tabs/ProcessingTab'
import { VersionsTab } from './components/tabs/VersionsTab'
import { EventsTab } from './components/tabs/EventsTab'
import { RawTab } from './components/tabs/RawTab'
import { AuditTab } from './components/tabs/AuditTab'
import { useAssetInspection } from './hooks/useAssetInspection'
import { useAssetEditor } from './hooks/useAssetEditor'
import { TabKey } from './types'
import { triggerClipAction } from '../../../api/client'

export default function AssetInspectorPage() {
  const { id } = useParams<{ id: string }>()
  const [activeTab, setActiveTab] = useState<TabKey>('generale')
  const [saveMsg, setSaveMsg] = useState<{ type: 'ok' | 'err'; text: string } | null>(null)

  const {
    asset,
    loading,
    error,
    refresh,
    actions,
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

  const runAction = useCallback(
    async (url?: string) => {
      if (!url) return
      try {
        const res = await triggerClipAction(url)
        const msg =
          typeof res === 'object' && res !== null && 'message' in res
            ? String(res.message)
            : 'Azione completata'
        setSaveMsg({ type: 'ok', text: msg })
        setTimeout(() => refresh(), 1000)
      } catch (err) {
        setSaveMsg({ type: 'err', text: err instanceof Error ? err.message : 'Errore azione' })
      }
    },
    [refresh]
  )

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
        {activeTab === 'generale' && <GeneralTab form={form} updateForm={updateForm} />}
        {activeTab === 'metadata' && <PipelineTab asset={asset} />}
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
        {activeTab === 'files' && <StorageTab asset={asset} actions={actions} onAction={runAction} />}
        {activeTab === 'processing' && <ProcessingTab asset={asset} />}
        {activeTab === 'versions' && <VersionsTab asset={asset} />}
        {activeTab === 'azioni' && <EventsTab actions={actions} onAction={runAction} onUpdate={handleSaveInternal} />}
        {activeTab === 'raw' && <RawTab asset={asset} />}
        {activeTab === 'audit' && <AuditTab />}
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
