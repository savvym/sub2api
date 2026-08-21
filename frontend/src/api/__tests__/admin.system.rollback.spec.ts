import { beforeEach, describe, expect, it, vi } from 'vitest'

const { get, post } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
}))

vi.mock('../client', () => ({
  apiClient: {
    get,
    post,
  },
}))

import {
  getRollbackVersions,
  performUpdate,
  restartService,
  rollback,
  type RollbackVersionInfo
} from '@/api/admin/system'

describe('admin system rollback API', () => {
  beforeEach(() => {
    sessionStorage.clear()
    get.mockReset()
    post.mockReset()
    vi.restoreAllMocks()
    vi.spyOn(globalThis.crypto, 'randomUUID').mockReturnValue(
      '11111111-1111-4111-8111-111111111111'
    )
  })

  it('getRollbackVersions fetches the rollback version list', async () => {
    const versions: RollbackVersionInfo[] = [
      {
        version: '0.1.146',
        published_at: '2026-07-07T00:00:00Z',
        html_url: 'https://github.com/Wei-Shaw/sub2api/releases/tag/v0.1.146'
      }
    ]
    get.mockResolvedValue({ data: { versions } })

    const result = await getRollbackVersions()

    expect(get).toHaveBeenCalledWith('/admin/system/rollback-versions')
    expect(result.versions).toEqual(versions)
  })

  it('rollback posts the target version in the request body', async () => {
    post.mockResolvedValue({ data: { message: 'ok', need_restart: true } })

    const result = await rollback('0.1.146')

    expect(post).toHaveBeenCalledWith(
      '/admin/system/rollback',
      { version: '0.1.146' },
      {
        timeout: 15 * 60 * 1000,
        headers: {
          'Idempotency-Key': 'system-rollback-11111111-1111-4111-8111-111111111111'
        }
      }
    )
    expect(result.need_restart).toBe(true)
    expect(sessionStorage.length).toBe(0)
  })

  it('rollback without a version posts no body (legacy backup rollback)', async () => {
    post.mockResolvedValue({ data: { message: 'ok', need_restart: true } })

    await rollback()

    expect(post).toHaveBeenCalledWith(
      '/admin/system/rollback',
      undefined,
      {
        timeout: 15 * 60 * 1000,
        headers: {
          'Idempotency-Key': 'system-rollback-11111111-1111-4111-8111-111111111111'
        }
      }
    )
  })

  it('sends idempotency keys for update and restart', async () => {
    post.mockResolvedValue({ data: { message: 'ok', need_restart: true } })

    await performUpdate()
    const restartResult = await restartService()

    expect(post).toHaveBeenNthCalledWith(1, '/admin/system/update', undefined, {
      timeout: 15 * 60 * 1000,
      headers: {
        'Idempotency-Key': 'system-update-11111111-1111-4111-8111-111111111111'
      }
    })
    expect(post).toHaveBeenNthCalledWith(2, '/admin/system/restart', undefined, {
      headers: {
        'Idempotency-Key': 'system-restart-11111111-1111-4111-8111-111111111111'
      }
    })
    expect(restartResult.confirmation).toBe('confirmed')
    expect(sessionStorage.length).toBe(0)
  })

  it('keeps the pending restart key when the server returns an HTTP error', async () => {
    const responseError = { status: 503, message: 'restart unavailable' }
    post.mockRejectedValueOnce(responseError)

    await expect(restartService()).rejects.toBe(responseError)

    const config = post.mock.calls[0][2]
    expect(sessionStorage.getItem('sub2api:admin:system-operation:restart')).toBe(
      config.headers['Idempotency-Key']
    )
  })

  it.each([
    ['without a response', new Error('connection closed')],
    ['with status zero', { status: 0, message: 'connection closed' }]
  ])('consumes the pending restart key on an ambiguous failure %s', async (_, error) => {
    post.mockRejectedValueOnce(error)

    const result = await restartService()

    expect(result).toEqual({ message: 'connection closed', confirmation: 'unknown' })
    expect(sessionStorage.getItem('sub2api:admin:system-operation:restart')).toBeNull()
  })

  it('reuses a pending rollback key after an ambiguous failure and reload', async () => {
    post.mockRejectedValueOnce(new Error('network timeout'))
    await expect(rollback('0.1.146')).rejects.toThrow('network timeout')
    const firstConfig = post.mock.calls[0][2]
    expect(sessionStorage.getItem('sub2api:admin:system-operation:rollback:0.1.146')).toBe(
      firstConfig.headers['Idempotency-Key']
    )

    vi.resetModules()
    post.mockResolvedValueOnce({ data: { message: 'ok', need_restart: true } })
    const { rollback: rollbackAfterReload } = await import('@/api/admin/system')
    await rollbackAfterReload('0.1.146')

    expect(post.mock.calls[1][2].headers).toEqual(firstConfig.headers)
    expect(sessionStorage.length).toBe(0)
  })
})
