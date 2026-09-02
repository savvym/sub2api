/**
 * System API endpoints for admin operations
 */

import { apiClient } from '../client'

export interface ReleaseInfo {
  name: string
  body: string
  published_at: string
  html_url: string
}

export interface VersionInfo {
  current_version: string
  latest_version: string
  has_update: boolean
  release_info?: ReleaseInfo
  cached: boolean
  warning?: string
  build_type: string // "source" for manual builds, "release" for CI builds
}

/**
 * Get current version
 */
export async function getVersion(): Promise<{ version: string }> {
  const { data } = await apiClient.get<{ version: string }>('/admin/system/version')
  return data
}

/**
 * Check for updates
 * @param force - Force refresh from GitHub API
 */
export async function checkUpdates(force = false): Promise<VersionInfo> {
  const { data } = await apiClient.get<VersionInfo>('/admin/system/check-updates', {
    params: force ? { force: 'true' } : undefined
  })
  return data
}

export interface UpdateResult {
  message: string
  need_restart: boolean
}

export interface RestartResult {
  message: string
  confirmation: 'confirmed' | 'unknown'
}

export interface RollbackVersionInfo {
  version: string
  published_at: string
  html_url: string
}

/**
 * Get versions available for rollback (up to 3 versions older than current)
 */
export async function getRollbackVersions(): Promise<{ versions: RollbackVersionInfo[] }> {
  const { data } = await apiClient.get<{ versions: RollbackVersionInfo[] }>(
    '/admin/system/rollback-versions'
  )
  return data
}

/**
 * In-place update/rollback downloads a full release binary from GitHub, which
 * can take several minutes on slow links. The global 30s axios timeout would
 * abort the request mid-download (#4504), so these calls wait as long as the
 * backend allows (15 minutes server-side).
 */
const UPDATE_REQUEST_TIMEOUT_MS = 15 * 60 * 1000

const pendingSystemOperationKeys = new Map<string, string>()

function systemOperationStorageKey(scope: string): string {
  return `sub2api:admin:system-operation:${scope}`
}

function getStoredSystemOperationKey(scope: string): string | null {
  try {
    return globalThis.sessionStorage?.getItem(systemOperationStorageKey(scope)) ?? null
  } catch {
    return null
  }
}

function storeSystemOperationKey(scope: string, key: string | null): void {
  try {
    const storageKey = systemOperationStorageKey(scope)
    if (key) globalThis.sessionStorage?.setItem(storageKey, key)
    else globalThis.sessionStorage?.removeItem(storageKey)
  } catch {
    // The in-memory key still protects retries when browser storage is unavailable.
  }
}

function pendingSystemOperationKey(scope: string, operation: string): string {
  let key = pendingSystemOperationKeys.get(scope) ?? getStoredSystemOperationKey(scope)
  if (!key) {
    const requestID =
      globalThis.crypto?.randomUUID?.() ?? `${Date.now()}-${Math.random().toString(36).slice(2)}`
    key = `system-${operation}-${requestID}`
  }
  pendingSystemOperationKeys.set(scope, key)
  storeSystemOperationKey(scope, key)
  return key
}

function clearPendingSystemOperationKey(scope: string, key: string): void {
  if (pendingSystemOperationKeys.get(scope) === key) {
    pendingSystemOperationKeys.delete(scope)
  }
  if (getStoredSystemOperationKey(scope) === key) {
    storeSystemOperationKey(scope, null)
  }
}

/**
 * Perform system update
 * Downloads and applies the latest version
 */
export async function performUpdate(): Promise<UpdateResult> {
  const scope = 'update'
  const idempotencyKey = pendingSystemOperationKey(scope, 'update')
  const { data } = await apiClient.post<UpdateResult>('/admin/system/update', undefined, {
    timeout: UPDATE_REQUEST_TIMEOUT_MS,
    headers: { 'Idempotency-Key': idempotencyKey }
  })
  clearPendingSystemOperationKey(scope, idempotencyKey)
  return data
}

/**
 * Rollback to a previous version
 * @param version - Target version (e.g. "0.1.146"); omit to restore the local backup binary
 */
export async function rollback(version?: string): Promise<UpdateResult> {
  const targetVersion = version?.trim() || undefined
  const scope = `rollback:${targetVersion ?? 'backup'}`
  const idempotencyKey = pendingSystemOperationKey(scope, 'rollback')
  const { data } = await apiClient.post<UpdateResult>(
    '/admin/system/rollback',
    targetVersion ? { version: targetVersion } : undefined,
    {
      timeout: UPDATE_REQUEST_TIMEOUT_MS,
      headers: { 'Idempotency-Key': idempotencyKey }
    }
  )
  clearPendingSystemOperationKey(scope, idempotencyKey)
  return data
}

/**
 * Restart the service
 */
export async function restartService(): Promise<RestartResult> {
  const scope = 'restart'
  const idempotencyKey = pendingSystemOperationKey(scope, 'restart')
  try {
    const { data } = await apiClient.post<{ message: string }>(
      '/admin/system/restart',
      undefined,
      {
        headers: { 'Idempotency-Key': idempotencyKey }
      }
    )
    clearPendingSystemOperationKey(scope, idempotencyKey)
    return { ...data, confirmation: 'confirmed' }
  } catch (error: unknown) {
    const requestError = error as {
      status?: number
      message?: string
      response?: { status?: number }
    }
    const responseStatus = requestError.response?.status ?? requestError.status
    if (responseStatus === undefined || responseStatus === 0) {
      clearPendingSystemOperationKey(scope, idempotencyKey)
      return {
        message: requestError.message || 'Restart response was not received',
        confirmation: 'unknown'
      }
    }
    throw error
  }
}

export const systemAPI = {
  getVersion,
  checkUpdates,
  performUpdate,
  getRollbackVersions,
  rollback,
  restartService
}

export default systemAPI
