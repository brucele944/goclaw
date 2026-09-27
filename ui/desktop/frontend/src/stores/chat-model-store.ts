import { create } from 'zustand'

/**
 * Per-request chat model override. Empty string = no override, so `chat.send`
 * omits `model` and the agent's configured model is used.
 */
interface ChatModelState {
  modelOverride: string
  setModelOverride: (model: string) => void
  clearModelOverride: () => void
}

export const useChatModelStore = create<ChatModelState>((set) => ({
  modelOverride: '',
  setModelOverride: (model) => set({ modelOverride: model }),
  clearModelOverride: () => set({ modelOverride: '' }),
}))
