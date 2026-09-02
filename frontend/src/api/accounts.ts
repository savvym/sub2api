/**
 * Regular-user account hosting API. The backend owns the product allowlist and
 * returns a narrow projection that never includes credentials or owner IDs.
 */

import { apiClient } from './client'
import type {
  CreateSelfServiceAccountRequest,
  PaginatedResponse,
  RenameSelfServiceAccountRequest,
  SelfServiceAccount,
  SelfServiceAccountListParams,
  SelfServiceAccountProduct,
} from '@/types'

export async function list(
  params: SelfServiceAccountListParams = {},
  options?: { signal?: AbortSignal },
): Promise<PaginatedResponse<SelfServiceAccount>> {
  const { data } = await apiClient.get<PaginatedResponse<SelfServiceAccount>>('/accounts', {
    params,
    signal: options?.signal,
  })
  return data
}

export async function getById(id: number): Promise<SelfServiceAccount> {
  const { data } = await apiClient.get<SelfServiceAccount>(`/accounts/${id}`)
  return data
}

export async function listProducts(): Promise<SelfServiceAccountProduct[]> {
  const { data } = await apiClient.get<SelfServiceAccountProduct[]>('/accounts/products')
  return data
}

export async function create(
  request: CreateSelfServiceAccountRequest,
): Promise<SelfServiceAccount> {
  const { data } = await apiClient.post<SelfServiceAccount>('/accounts', request)
  return data
}

export async function rename(
  id: number,
  request: RenameSelfServiceAccountRequest,
): Promise<SelfServiceAccount> {
  const { data } = await apiClient.patch<SelfServiceAccount>(`/accounts/${id}`, request)
  return data
}

export async function deleteAccount(id: number): Promise<SelfServiceAccount> {
  const { data } = await apiClient.delete<SelfServiceAccount>(`/accounts/${id}`)
  return data
}

export const selfServiceAccountsAPI = {
  list,
  getById,
  listProducts,
  create,
  rename,
  delete: deleteAccount,
}

export default selfServiceAccountsAPI
