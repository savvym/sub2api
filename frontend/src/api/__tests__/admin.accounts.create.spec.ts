import { beforeEach, describe, expect, it, vi } from 'vitest'

const { post } = vi.hoisted(() => ({ post: vi.fn() }))

vi.mock('@/api/client', () => ({ apiClient: { post } }))

import { create, createInGroup } from '@/api/admin/accounts'
import type { CreateAccountRequest } from '@/types'

const payload = (name: string): CreateAccountRequest => ({
  name,
  platform: 'openai',
  type: 'apikey',
  credentials: { api_key: 'secret' },
  extra: {},
  proxy_id: null,
  concurrency: 1,
  priority: 1,
  group_ids: [42],
  expires_at: null
})

describe('admin account create idempotency', () => {
  beforeEach(() => {
    post.mockReset()
  })

  it('reuses the operation key after an ambiguous network failure', async () => {
    post.mockRejectedValueOnce({ status: 0, message: 'timeout' })
    await expect(create(payload('ambiguous-create'))).rejects.toMatchObject({ status: 0 })
    const firstKey = post.mock.calls[0][2].headers['Idempotency-Key']

    post.mockResolvedValueOnce({ data: { id: 7 } })
    await create(payload('ambiguous-create'))

    expect(post.mock.calls[1][2].headers['Idempotency-Key']).toBe(firstKey)
  })

  it('uses a fresh key after success and when confirmation changes the payload', async () => {
    post.mockResolvedValue({ data: { id: 8 } })
    const initial = payload('confirmed-create')
    await create(initial)
    const firstKey = post.mock.calls[0][2].headers['Idempotency-Key']

    await create(initial)
    const secondKey = post.mock.calls[1][2].headers['Idempotency-Key']
    expect(secondKey).not.toBe(firstKey)

    await create({ ...initial, confirm_mixed_channel_risk: true })
    expect(post.mock.calls[2][2].headers['Idempotency-Key']).not.toBe(secondKey)
  })

  it('posts group-scoped creates with retry-safe keys derived from the full payload', async () => {
    const initial = payload('group-create')
    post.mockRejectedValueOnce({ status: 0, message: 'timeout' })
    await expect(createInGroup(42, initial)).rejects.toMatchObject({ status: 0 })

    expect(post.mock.calls[0][0]).toBe('/admin/groups/42/accounts')
    expect(post.mock.calls[0][1]).toEqual(initial)
    const initialKey = post.mock.calls[0][2].headers['Idempotency-Key']

    post.mockResolvedValueOnce({ data: { id: 9 } })
    await createInGroup(42, { ...initial, risk_confirmation_token: 'risk-token' })
    expect(post.mock.calls[1][1]).toEqual({ ...initial, risk_confirmation_token: 'risk-token' })
    expect(post.mock.calls[1][2].headers['Idempotency-Key']).not.toBe(initialKey)

    post.mockResolvedValueOnce({ data: { id: 10 } })
    await createInGroup(42, initial)
    expect(post.mock.calls[2][2].headers['Idempotency-Key']).toBe(initialKey)
  })

  it.each(['IDEMPOTENCY_IN_PROGRESS', 'IDEMPOTENCY_RETRY_BACKOFF'])(
    'retains the group-create key after %s',
    async (reason) => {
      const initial = payload(`group-create-${reason}`)
      post.mockRejectedValueOnce({ status: 409, reason })
      await expect(createInGroup(42, initial)).rejects.toMatchObject({ status: 409, reason })
      const firstKey = post.mock.calls[0][2].headers['Idempotency-Key']

      post.mockResolvedValueOnce({ data: { id: 11 } })
      await createInGroup(42, initial)

      expect(post.mock.calls[1][2].headers['Idempotency-Key']).toBe(firstKey)
    }
  )
})
