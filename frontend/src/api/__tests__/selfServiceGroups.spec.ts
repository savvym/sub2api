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

import { selfServiceGroupsAPI } from '@/api/selfServiceGroups'

describe('self-service groups api', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    get.mockResolvedValue({ data: {} })
    post.mockResolvedValue({ data: {} })
    patch.mockResolvedValue({ data: {} })
    deleteRequest.mockResolvedValue({ data: {} })
  })

  it('lists groups with the requested query and abort signal', async () => {
    const signal = new AbortController().signal
    const params = {
      page: 2,
      page_size: 50,
      search: 'personal',
      sort_by: 'name' as const,
      sort_order: 'asc' as const,
    }

    await selfServiceGroupsAPI.list(params, { signal })

    expect(get).toHaveBeenCalledWith('/groups', { params, signal })
  })

  it('gets one group by id', async () => {
    await selfServiceGroupsAPI.getById(42)

    expect(get).toHaveBeenCalledWith('/groups/42')
  })

  it('loads the server-owned platform catalog', async () => {
    await selfServiceGroupsAPI.listPlatforms()

    expect(get).toHaveBeenCalledWith('/groups/platforms')
  })

  it('creates a group with only the accepted platform selection and descriptive fields', async () => {
    const request = {
      name: 'Personal OpenAI',
      description: 'Private routing group',
      platform_id: 'openai',
    }

    await selfServiceGroupsAPI.create(request)

    expect(post).toHaveBeenCalledWith('/groups', request)
    expect(Object.keys(post.mock.calls[0][1])).toEqual(['name', 'description', 'platform_id'])
  })

  it('updates only the accepted mutable fields', async () => {
    const request = { description: 'Updated description' }

    await selfServiceGroupsAPI.update(42, request)

    expect(patch).toHaveBeenCalledWith('/groups/42', request)
    expect(Object.keys(patch.mock.calls[0][1])).toEqual(['description'])
  })

  it('deletes a group by id', async () => {
    await selfServiceGroupsAPI.delete(42)

    expect(deleteRequest).toHaveBeenCalledWith('/groups/42')
  })
})
