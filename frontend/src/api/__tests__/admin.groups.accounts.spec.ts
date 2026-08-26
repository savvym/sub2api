import { beforeEach, describe, expect, it, vi } from 'vitest'

const { get, patch } = vi.hoisted(() => ({
  get: vi.fn(),
  patch: vi.fn()
}))

vi.mock('@/api/client', () => ({
  apiClient: { get, patch }
}))

import { listAccountCandidates, listAccounts, updateAccounts } from '@/api/admin/groups'

describe('admin group accounts API', () => {
  beforeEach(() => {
    get.mockReset()
    patch.mockReset()
    get.mockResolvedValue({ data: { items: [], total: 0, page: 1, page_size: 20, pages: 0 } })
    patch.mockResolvedValue({ data: { account_count: 0 } })
  })

  it('sends independent server-side member and candidate queries', async () => {
    const controller = new AbortController()
    await listAccounts(42, 2, 50, { search: 'primary', status: 'active' }, { signal: controller.signal })
    await listAccountCandidates(42, 3, 20, { type: 'oauth', platform: 'openai' })

    expect(get).toHaveBeenNthCalledWith(1, '/admin/groups/42/accounts', {
      params: { page: 2, page_size: 50, search: 'primary', status: 'active' },
      signal: controller.signal
    })
    expect(get).toHaveBeenNthCalledWith(2, '/admin/groups/42/account-candidates', {
      params: { page: 3, page_size: 20, type: 'oauth', platform: 'openai' },
      signal: undefined
    })
  })

  it('sends a scoped diff with its idempotency key', async () => {
    await updateAccounts(
      42,
      {
        add_account_ids: [1, 2],
        remove_account_ids: [3],
        risk_confirmation_token: 'risk-token'
      },
      { idempotencyKey: 'operation-key' }
    )

    expect(patch).toHaveBeenCalledWith(
      '/admin/groups/42/accounts',
      {
        add_account_ids: [1, 2],
        remove_account_ids: [3],
        risk_confirmation_token: 'risk-token'
      },
      { headers: { 'Idempotency-Key': 'operation-key' } }
    )
  })
})
