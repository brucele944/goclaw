import { useState, useEffect, useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { Combobox } from '../common/Combobox'
import { CatalogueNotice } from './CatalogueNotice'
import { useProviderCapabilities } from '../../hooks/use-provider-capabilities'
import { isCatalogueStale, modelOptionsFor, providerOptionsFrom } from '../../api/provider-capabilities'
import { getApiClient } from '../../lib/api'

interface ModelBudgetSectionProps {
  provider: string
  model: string
  contextWindow: number
  maxToolIterations: number
  savedProvider: string
  savedModel: string
  onProviderChange: (v: string) => void
  onModelChange: (v: string) => void
  onContextWindowChange: (v: number) => void
  onMaxToolIterationsChange: (v: number) => void
  onSaveBlockedChange: (blocked: boolean) => void
}

export function ModelBudgetSection({
  provider, model, contextWindow, maxToolIterations,
  savedProvider, savedModel,
  onProviderChange, onModelChange, onContextWindowChange, onMaxToolIterationsChange,
  onSaveBlockedChange,
}: ModelBudgetSectionProps) {
  const { t } = useTranslation(['agents', 'common'])
  const { capabilities, isLoading, error, refetch } = useProviderCapabilities()
  const [verifyResult, setVerifyResult] = useState<{ valid: boolean; error?: string } | null>(null)
  const [verifying, setVerifying] = useState(false)

  // Provider identity in agent configs is the provider name (`capabilities[].id`).
  const capability = useMemo(
    () => capabilities.find((c) => c.id === provider),
    [capabilities, provider],
  )

  const providerOptions = useMemo(() => providerOptionsFrom(capabilities), [capabilities])
  const modelOptions = useMemo(() => modelOptionsFor(capability), [capability])
  const stale = isCatalogueStale(capability)

  // Show verify button when provider/model changed from saved
  const needsVerify = (provider !== savedProvider || model !== savedModel) && provider && model
  useEffect(() => {
    if (needsVerify) {
      setVerifyResult(null)
      onSaveBlockedChange(true)
    } else {
      onSaveBlockedChange(false)
    }
  }, [needsVerify, onSaveBlockedChange])

  const handleVerify = async () => {
    const providerRef = capability?.provider_id ?? capability?.id
    if (!providerRef || !model.trim()) return
    setVerifying(true)
    try {
      const res = await getApiClient().post<{ success: boolean; error?: string }>(
        `/v1/providers/${providerRef}/verify`,
        { model: model.trim() },
      )
      setVerifyResult({ valid: res.success, error: res.error })
      onSaveBlockedChange(!res.success)
    } catch (err) {
      setVerifyResult({ valid: false, error: err instanceof Error ? err.message : 'Verification failed' })
    } finally {
      setVerifying(false)
    }
  }

  return (
    <div className="space-y-4">
      <h3 className="text-sm font-semibold text-text-primary">{t('agents:detail.modelBudget')}</h3>

      {/* Provider + Model */}
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <div className="space-y-1">
          <label className="text-xs font-medium text-text-secondary">{t('common:provider')}</label>
          <Combobox value={provider} onChange={onProviderChange} options={providerOptions} placeholder={t('agents:create.selectProvider')} />
        </div>
        <div className="space-y-1">
          <label className="text-xs font-medium text-text-secondary">{t('common:model')}</label>
          <Combobox
            value={model}
            onChange={onModelChange}
            options={modelOptions}
            placeholder={isLoading ? t('common:loading') : t('agents:create.enterOrSelectModel')}
            loading={isLoading}
            allowCustom
          />
        </div>
      </div>

      <CatalogueNotice stale={stale} error={error} onRetry={() => { void refetch() }} />

      {/* Verify button + result */}
      {needsVerify && (
        <div className="flex items-center gap-3">
          <button
            onClick={handleVerify}
            disabled={verifying}
            className="text-xs px-3 py-1.5 border border-accent text-accent rounded-lg hover:bg-accent/10 transition-colors disabled:opacity-50"
          >
            {verifying ? t('agents:create.checking') : t('agents:create.check')}
          </button>
          {verifyResult && (
            <span className={`text-xs ${verifyResult.valid ? 'text-success' : 'text-error'}`}>
              {verifyResult.valid ? t('common:modelVerified') : verifyResult.error}
            </span>
          )}
          {!verifyResult && (
            <span className="text-[11px] text-warning">{t('common:checkAndCreate')}</span>
          )}
        </div>
      )}

      {/* Context window + Max iterations */}
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <div className="space-y-1">
          <label className="text-xs font-medium text-text-secondary">{t('agents:llmConfig.contextWindow')}</label>
          <input
            type="number"
            value={contextWindow || ''}
            onChange={(e) => onContextWindowChange(Number(e.target.value) || 0)}
            placeholder="200000"
            className="w-full bg-surface-tertiary border border-border rounded-lg px-3 py-2 text-base md:text-sm text-text-primary placeholder:text-text-muted focus:outline-none focus:ring-1 focus:ring-accent"
          />
          <p className="text-[10px] text-text-muted">{t('agents:llmConfig.contextWindowHint')}</p>
        </div>
        <div className="space-y-1">
          <label className="text-xs font-medium text-text-secondary">{t('agents:llmConfig.maxToolIterations')}</label>
          <input
            type="number"
            value={maxToolIterations || ''}
            onChange={(e) => onMaxToolIterationsChange(Number(e.target.value) || 0)}
            placeholder="25"
            className="w-full bg-surface-tertiary border border-border rounded-lg px-3 py-2 text-base md:text-sm text-text-primary placeholder:text-text-muted focus:outline-none focus:ring-1 focus:ring-accent"
          />
          <p className="text-[10px] text-text-muted">{t('agents:llmConfig.maxToolIterationsHint')}</p>
        </div>
      </div>
    </div>
  )
}
