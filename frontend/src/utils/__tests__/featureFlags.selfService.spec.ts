import { beforeEach, describe, expect, it, vi } from 'vitest'

const appStore = vi.hoisted(() => ({
  cachedPublicSettings: null as null | Record<string, unknown>,
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => appStore,
}))

import {
  FeatureFlags,
  isFeatureFlagEnabled,
  makeSidebarFlag,
} from '@/utils/featureFlags'

describe('self-service account feature flag', () => {
  beforeEach(() => {
    appStore.cachedPublicSettings = null
  })

  it('fails closed while public settings are missing', () => {
    expect(FeatureFlags.selfServiceAccounts.mode).toBe('opt-in')
    expect(isFeatureFlagEnabled(FeatureFlags.selfServiceAccounts)).toBe(false)
    expect(makeSidebarFlag(FeatureFlags.selfServiceAccounts)()).toBe(false)
  })

  it('is enabled only by an explicit effective true value', () => {
    appStore.cachedPublicSettings = { self_service_hosting_enabled: true }
    expect(isFeatureFlagEnabled(FeatureFlags.selfServiceAccounts)).toBe(true)

    appStore.cachedPublicSettings = { self_service_hosting_enabled: false }
    expect(isFeatureFlagEnabled(FeatureFlags.selfServiceAccounts)).toBe(false)

    appStore.cachedPublicSettings = {}
    expect(isFeatureFlagEnabled(FeatureFlags.selfServiceAccounts)).toBe(false)
  })
})
