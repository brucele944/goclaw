/** Per-model catalogue entry from GET /v1/providers/capabilities. */
export interface ProviderModelCapability {
  /** Full `<provider>/<model>` identity as served by the gateway. */
  id: string
  label?: string
  context_window?: number
  max_tokens?: number
  thinking_levels?: string[]
  default_thinking_level?: string
  supports_fast_mode?: boolean
  capabilities?: {
    tool_calling?: boolean
    vision?: boolean
    stream_with_tools?: boolean
    cache_control?: boolean
  }
  cost?: { input?: number; output?: number }
  /** True when the gateway could not refresh this entry from upstream. */
  stale?: boolean
}

/** Provider entry from GET /v1/providers/capabilities (Viewer role). */
export interface ProviderCapability {
  /** Provider name, e.g. "groq" — the value stored in agent configs. */
  id: string
  provider_id?: string
  label?: string
  wire_api?: string
  auth_kind?: string
  model_source?: string
  default_model_id?: string
  models?: ProviderModelCapability[]
}

export interface ProviderData {
  id: string
  name: string
  display_name?: string
  provider_type: string
  api_base?: string
  api_key?: string
  enabled: boolean
  settings?: Record<string, unknown>
  created_at?: string
  updated_at?: string
  // --- Provider subsystem rework: transport / auth / catalogue metadata ---
  /** Transport family, e.g. `openai-completions`, `anthropic-messages`, `cli-delegated`. */
  wire_api?: string
  /** Credential kind, e.g. `api_key`, `oauth_browser`, `service_account`, `cli_delegated`, `none`. */
  auth_kind?: string
  /** Where the model catalogue came from: `bundled`, `discovered`, or a stale marker. */
  model_source?: string
  /** Default `<provider>/<model>` identity for this provider. */
  default_model_id?: string
  /** Per-model catalogue (populated from the capabilities endpoint). */
  models?: ProviderModelCapability[]
}

export interface ProviderInput {
  name: string
  display_name?: string
  provider_type: string
  api_base?: string
  api_key?: string
  enabled?: boolean
  settings?: Record<string, unknown>
}
