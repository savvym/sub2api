/**
 * Regular-user group hosting API. Platform policy is selected only through
 * the server-owned catalog; pricing, routing, subscriptions, and account
 * topology are intentionally absent from this contract.
 */

import { apiClient } from './client'
import type {
  CreateSelfServiceGroupRequest,
  PaginatedResponse,
  SelfServiceGroup,
  SelfServiceGroupListParams,
  SelfServiceGroupPlatform,
  UpdateSelfServiceGroupRequest,
} from '@/types'

export async function list(
  params: SelfServiceGroupListParams = {},
  options?: { signal?: AbortSignal },
): Promise<PaginatedResponse<SelfServiceGroup>> {
  const { data } = await apiClient.get<PaginatedResponse<SelfServiceGroup>>('/groups', {
    params,
    signal: options?.signal,
  })
  return data
}

export async function getById(id: number): Promise<SelfServiceGroup> {
  const { data } = await apiClient.get<SelfServiceGroup>(`/groups/${id}`)
  return data
}

export async function listPlatforms(): Promise<SelfServiceGroupPlatform[]> {
  const { data } = await apiClient.get<SelfServiceGroupPlatform[]>('/groups/platforms')
  return data
}

export async function create(
  request: CreateSelfServiceGroupRequest,
): Promise<SelfServiceGroup> {
  const { data } = await apiClient.post<SelfServiceGroup>('/groups', request)
  return data
}

export async function update(
  id: number,
  request: UpdateSelfServiceGroupRequest,
): Promise<SelfServiceGroup> {
  const { data } = await apiClient.patch<SelfServiceGroup>(`/groups/${id}`, request)
  return data
}

export async function deleteGroup(id: number): Promise<SelfServiceGroup> {
  const { data } = await apiClient.delete<SelfServiceGroup>(`/groups/${id}`)
  return data
}

export const selfServiceGroupsAPI = {
  list,
  getById,
  listPlatforms,
  create,
  update,
  delete: deleteGroup,
}

export default selfServiceGroupsAPI
