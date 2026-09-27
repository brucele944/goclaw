import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'

const { call, on } = vi.hoisted(() => ({
  call: vi.fn(async (..._args: unknown[]) => ({})),
  on: vi.fn(() => () => {}),
}))

vi.mock('../lib/ws', () => ({ getWsClient: () => ({ call, on }) }))

import { useChat } from '../hooks/use-chat'
import { useChatModelStore } from '../stores/chat-model-store'
import { useSessionStore } from '../stores/session-store'
import { useChatMessageStore } from '../stores/chat-message-store'

function lastChatSendParams(): Record<string, unknown> | undefined {
  const send = call.mock.calls.find(([method]) => method === 'chat.send')
  return send?.[1] as Record<string, unknown> | undefined
}

describe('chat.send per-request model override', () => {
  beforeEach(() => {
    call.mockClear()
    on.mockClear()
    useChatMessageStore.getState().clear()
    useSessionStore.setState({ activeSessionKey: 'sess-1' })
    useChatModelStore.setState({ modelOverride: '' })
  })

  it('sends the <provider>/<model> identity when the selector has a value', async () => {
    useChatModelStore.setState({ modelOverride: 'groq/llama-3.3-70b' })
    const { result } = renderHook(() => useChat())

    await act(async () => { await result.current.sendMessage('hello', 'agent-1') })

    expect(lastChatSendParams()).toMatchObject({
      message: 'hello',
      agentId: 'agent-1',
      sessionKey: 'sess-1',
      stream: true,
      model: 'groq/llama-3.3-70b',
    })
  })

  it('omits model when the override is cleared, falling back to the agent model', async () => {
    const { result } = renderHook(() => useChat())

    await act(async () => { await result.current.sendMessage('hello', 'agent-1') })

    const params = lastChatSendParams()
    expect(params).toBeDefined()
    expect(params).not.toHaveProperty('model')
  })
})
