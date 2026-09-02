import { beforeEach, describe, expect, it, vi } from 'vitest'

const { get, post, patch, deleteRequest } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  patch: vi.fn(),
  deleteRequest: vi.fn(),
}))

vi.mock('@/api/client', () => ({
  apiClient: {
    get,
    post,
    patch,
    delete: deleteRequest,
  },
}))

import { selfServiceAccountsAPI } from '@/api/accounts'

describe('self-service accounts api', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    get.mockResolvedValue({ data: {} })
    post.mockResolvedValue({ data: {} })
    patch.mockResolvedValue({ data: {} })
    deleteRequest.mockResolvedValue({ data: {} })
  })

  it('lists accounts with the requested query and abort signal', async () => {
    const signal = new AbortController().signal
    const params = {
      page: 2,
      page_size: 50,
      search: 'personal',
      sort_by: 'name' as const,
      sort_order: 'asc' as const,
    }

    await selfServiceAccountsAPI.list(params, { signal })

    expect(get).toHaveBeenCalledWith('/accounts', { params, signal })
  })

  it('gets one account by id', async () => {
    await selfServiceAccountsAPI.getById(42)

    expect(get).toHaveBeenCalledWith('/accounts/42')
  })

  it('loads the server-owned product catalog', async () => {
    await selfServiceAccountsAPI.listProducts()

    expect(get).toHaveBeenCalledWith('/accounts/products')
  })

  it('creates an account with only the accepted credential payload', async () => {
    const request = {
      name: 'Personal OpenAI',
      product_id: 'openai-api-key',
      api_key: 'sk-secret',
    }

    await selfServiceAccountsAPI.create(request)

    expect(post).toHaveBeenCalledWith('/accounts', request)
    expect(Object.keys(post.mock.calls[0][1])).toEqual(['name', 'product_id', 'api_key'])
  })

  it('renames an account with a name-only patch', async () => {
    await selfServiceAccountsAPI.rename(42, { name: 'Renamed' })

    expect(patch).toHaveBeenCalledWith('/accounts/42', { name: 'Renamed' })
  })

  it('deletes an account by id', async () => {
    await selfServiceAccountsAPI.delete(42)

    expect(deleteRequest).toHaveBeenCalledWith('/accounts/42')
  })
})
