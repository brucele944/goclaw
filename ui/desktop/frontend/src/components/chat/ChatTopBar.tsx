import { useState, useRef, useEffect, useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { useAgents } from '../../hooks/use-agents'
import { useProviderCapabilities } from '../../hooks/use-provider-capabilities'
import { isCatalogueStale, modelOptionsFor } from '../../api/provider-capabilities'
import { useChatModelStore } from '../../stores/chat-model-store'
import { useUiStore } from '../../stores/ui-store'
import { LANGUAGES, getAllTimezones } from '../../lib/constants'
import { Combobox } from '../common/Combobox'

/**
 * Per-request model override for `chat.send`. Value is a `<provider>/<model>`
 * identity; clearing it sends no `model` so the agent's own model is used.
 */
function ModelOverridePicker() {
  const { t } = useTranslation('chat')
  const { capabilities, isLoading, error, refetch } = useProviderCapabilities()
  const modelOverride = useChatModelStore((s) => s.modelOverride)
  const setModelOverride = useChatModelStore((s) => s.setModelOverride)
  const clearModelOverride = useChatModelStore((s) => s.clearModelOverride)

  const options = useMemo(
    () => capabilities.flatMap((c) => modelOptionsFor(c)),
    [capabilities],
  )
  const stale = useMemo(() => capabilities.some((c) => isCatalogueStale(c)), [capabilities])

  return (
    <div className="wails-no-drag flex items-center gap-1 mr-1">
      <div className="w-44" title={t('modelOverride.label')}>
        <Combobox
          value={modelOverride}
          onChange={setModelOverride}
          options={options}
          placeholder={t('modelOverride.placeholder')}
          loading={isLoading}
        />
      </div>
      {modelOverride && (
        <button
          onClick={clearModelOverride}
          title={t('modelOverride.reset')}
          className="w-6 h-6 flex items-center justify-center rounded text-text-muted hover:text-text-primary hover:bg-surface-tertiary transition-colors"
        >
          <svg className="h-3.5 w-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round">
            <path d="M18 6 6 18" /><path d="m6 6 12 12" />
          </svg>
        </button>
      )}
      {(stale || error) && (
        <button
          onClick={() => { void refetch() }}
          title={stale ? t('modelOverride.stale') : t('modelOverride.loadFailed')}
          className="w-6 h-6 flex items-center justify-center rounded text-warning hover:bg-warning/10 transition-colors"
        >
          <svg className="h-3.5 w-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round">
            <path d="M12 9v4" /><path d="M12 17h.01" />
            <path d="M10.29 3.86 1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z" />
          </svg>
        </button>
      )}
    </div>
  )
}

function LanguagePicker() {
  const locale = useUiStore((s) => s.locale)
  const setLocale = useUiStore((s) => s.setLocale)
  const { i18n } = useTranslation()
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return
    const handler = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [open])

  const current = LANGUAGES.find((l) => l.value === locale) ?? LANGUAGES[0]

  function handleSelect(lang: string) {
    setLocale(lang)
    i18n.changeLanguage(lang)
    setOpen(false)
  }

  return (
    <div ref={ref} className="relative">
      <button
        onClick={() => setOpen(!open)}
        className="wails-no-drag flex items-center gap-1 px-2 py-1 rounded-lg text-xs text-text-muted hover:text-text-primary hover:bg-surface-tertiary transition-colors"
      >
        <span>{current.flag}</span>
        <span>{current.label}</span>
        <svg className="h-3 w-3" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round">
          <path d="m6 9 6 6 6-6" />
        </svg>
      </button>
      {open && (
        <div className="absolute right-0 top-full mt-1 bg-surface-secondary border border-border rounded-lg shadow-lg overflow-hidden z-50">
          {LANGUAGES.map((lang) => (
            <button
              key={lang.value}
              onClick={() => handleSelect(lang.value)}
              className={`w-full flex items-center gap-2 px-3 py-1.5 text-xs transition-colors ${
                locale === lang.value
                  ? 'bg-accent/10 text-accent font-medium'
                  : 'text-text-secondary hover:bg-surface-tertiary'
              }`}
            >
              <span>{lang.flag}</span>
              <span>{lang.label}</span>
            </button>
          ))}
        </div>
      )}
    </div>
  )
}

function TimezonePicker() {
  const timezone = useUiStore((s) => s.timezone)
  const setTimezone = useUiStore((s) => s.setTimezone)
  const [open, setOpen] = useState(false)
  const [search, setSearch] = useState('')
  const ref = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return
    const handler = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [open])

  const allTimezones = getAllTimezones()
  const filtered = search
    ? allTimezones.filter((tz: string) => tz.toLowerCase().includes(search.toLowerCase()))
    : allTimezones

  // Short display: "Asia/Saigon" → "Saigon"
  const shortTz = timezone.split('/').pop() ?? timezone

  return (
    <div ref={ref} className="relative">
      <button
        onClick={() => { setOpen(!open); setSearch('') }}
        className="wails-no-drag flex items-center gap-1 px-2 py-1 rounded-lg text-xs text-text-muted hover:text-text-primary hover:bg-surface-tertiary transition-colors"
        title={timezone}
      >
        <svg className="h-3 w-3" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round">
          <circle cx="12" cy="12" r="10" /><polyline points="12 6 12 12 16 14" />
        </svg>
        <span>{shortTz}</span>
        <svg className="h-3 w-3" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round">
          <path d="m6 9 6 6 6-6" />
        </svg>
      </button>
      {open && (
        <div className="absolute right-0 top-full mt-1 w-56 bg-surface-secondary border border-border rounded-lg shadow-lg overflow-hidden z-50">
          <div className="p-1.5">
            <input
              type="text"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="Search timezone..."
              className="w-full px-2 py-1 text-xs bg-surface-tertiary border border-border rounded text-text-primary placeholder:text-text-muted focus:outline-none focus:ring-1 focus:ring-accent"
              autoFocus
            />
          </div>
          <div className="max-h-48 overflow-y-auto overscroll-contain">
            {filtered.slice(0, 50).map((tz) => (
              <button
                key={tz}
                onClick={() => { setTimezone(tz); setOpen(false) }}
                className={`w-full text-left px-3 py-1 text-xs transition-colors ${
                  timezone === tz
                    ? 'bg-accent/10 text-accent font-medium'
                    : 'text-text-secondary hover:bg-surface-tertiary'
                }`}
              >
                {tz}
              </button>
            ))}
          </div>
        </div>
      )}
    </div>
  )
}

export function ChatTopBar() {
  const { selectedAgent } = useAgents()
  const toggleSidebar = useUiStore((s) => s.toggleSidebar)
  const sidebarOpen = useUiStore((s) => s.sidebarOpen)

  return (
    <div className="h-12 flex items-center px-4 shrink-0 mt-4">
      {/* Sidebar toggle */}
      {!sidebarOpen && (
        <button
          onClick={toggleSidebar}
          className="wails-no-drag w-7 h-7 flex items-center justify-center rounded-lg text-text-muted hover:text-text-primary hover:bg-surface-tertiary transition-colors mr-2"
          title="Show sidebar"
        >
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
            <line x1="3" y1="6" x2="21" y2="6" /><line x1="3" y1="12" x2="21" y2="12" /><line x1="3" y1="18" x2="21" y2="18" />
          </svg>
        </button>
      )}

      {/* Agent info */}
      <div className="flex-1 min-w-0">
        {selectedAgent ? (
          <div className="flex items-center gap-2">
            <span className="text-sm font-medium text-text-primary">{selectedAgent.name}</span>
            <span className="text-[11px] font-mono px-1.5 py-0.5 rounded bg-surface-tertiary text-text-muted">
              {selectedAgent.model}
            </span>
          </div>
        ) : (
          <span className="text-sm text-text-muted">Select an agent to start chatting</span>
        )}
      </div>

      {/* Top right pickers */}
      <div className="flex items-center gap-1">
        {selectedAgent && <ModelOverridePicker />}
        <TimezonePicker />
        <LanguagePicker />
      </div>
    </div>
  )
}
