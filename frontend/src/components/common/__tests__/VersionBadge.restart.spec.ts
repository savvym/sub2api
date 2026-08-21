import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import VersionBadge from '../VersionBadge.vue'

const apiMocks = vi.hoisted(() => ({
  performUpdate: vi.fn(),
  restartService: vi.fn(),
  getRollbackVersions: vi.fn(),
  rollback: vi.fn()
}))

const storeMocks = vi.hoisted(() => ({
  fetchVersion: vi.fn(),
  clearVersionCache: vi.fn()
}))

vi.mock('@/api/admin/system', () => apiMocks)

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({ copied: false, copyToClipboard: vi.fn() })
}))

vi.mock('@/stores', () => ({
  useAuthStore: () => ({ isAdmin: true }),
  useAppStore: () => ({
    versionLoading: false,
    currentVersion: '1.0.0',
    latestVersion: '1.1.0',
    hasUpdate: true,
    releaseInfo: null,
    buildType: 'release',
    fetchVersion: storeMocks.fetchVersion,
    clearVersionCache: storeMocks.clearVersionCache
  })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key })
  }
})

async function openRestartPanel() {
  const wrapper = mount(VersionBadge, {
    global: {
      stubs: { Icon: true }
    }
  })

  const versionButton = wrapper
    .findAll('button')
    .find((button) => button.text().includes('v1.0.0'))
  expect(versionButton).toBeDefined()
  await versionButton!.trigger('click')

  const updateButton = wrapper
    .findAll('button')
    .find((button) => button.text().includes('version.updateNow'))
  expect(updateButton).toBeDefined()
  await updateButton!.trigger('click')
  await flushPromises()

  return wrapper
}

describe('VersionBadge restart behavior', () => {
  beforeEach(() => {
    apiMocks.performUpdate.mockReset().mockResolvedValue({
      message: 'updated',
      need_restart: true
    })
    apiMocks.restartService.mockReset()
    apiMocks.getRollbackVersions.mockReset()
    apiMocks.rollback.mockReset()
    storeMocks.fetchVersion.mockReset().mockResolvedValue(undefined)
    storeMocks.clearVersionCache.mockReset()
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('shows an HTTP restart error and does not start the countdown', async () => {
    apiMocks.restartService.mockRejectedValue({ status: 503, message: 'restart unavailable' })
    const intervalSpy = vi.spyOn(globalThis, 'setInterval')
    const wrapper = await openRestartPanel()
    intervalSpy.mockClear()

    const restartButton = wrapper
      .findAll('button')
      .find((button) => button.text().includes('version.restartNow'))
    expect(restartButton).toBeDefined()
    await restartButton!.trigger('click')
    await flushPromises()

    expect(intervalSpy).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('version.restartFailed')
    expect(wrapper.text()).toContain('restart unavailable')
    expect(wrapper.text()).toContain('version.restartNow')
    expect(wrapper.text()).not.toContain('version.restarting')

    wrapper.unmount()
  })

  it('starts the countdown when restart confirmation is unknown', async () => {
    apiMocks.restartService.mockResolvedValue({
      message: 'connection closed',
      confirmation: 'unknown'
    })
    const intervalSpy = vi
      .spyOn(globalThis, 'setInterval')
      .mockImplementation(() => 1 as unknown as ReturnType<typeof setInterval>)
    const wrapper = await openRestartPanel()
    intervalSpy.mockClear()

    const restartButton = wrapper
      .findAll('button')
      .find((button) => button.text().includes('version.restartNow'))
    expect(restartButton).toBeDefined()
    await restartButton!.trigger('click')
    await flushPromises()

    expect(intervalSpy).toHaveBeenCalledWith(expect.any(Function), 1000)
    expect(wrapper.text()).toContain('version.restarting')
    expect(wrapper.text()).toContain('(8s)')
    expect(wrapper.text()).not.toContain('version.restartFailed')

    wrapper.unmount()
  })
})
