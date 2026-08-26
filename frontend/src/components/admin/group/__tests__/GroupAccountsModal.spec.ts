import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Account, AdminGroup, GroupAccountSummary } from '@/types'
import GroupAccountsModal from '@/components/admin/group/GroupAccountsModal.vue'

const {
  listAccounts,
  listAccountCandidates,
  updateAccounts,
  getAllGroups,
  getAllProxies,
  getAccountById,
  showSuccess,
  showError
} = vi.hoisted(() => ({
  listAccounts: vi.fn(),
  listAccountCandidates: vi.fn(),
  updateAccounts: vi.fn(),
  getAllGroups: vi.fn(),
  getAllProxies: vi.fn(),
  getAccountById: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    groups: {
      listAccounts,
      listAccountCandidates,
      updateAccounts,
      getAllIncludingInactive: getAllGroups
    },
    accounts: { getById: getAccountById },
    proxies: { getAll: getAllProxies }
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showSuccess, showError })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) =>
        params ? `${key}:${JSON.stringify(params)}` : key,
      te: () => false
    })
  }
})

vi.mock('@/components/account', () => ({
  CreateAccountModal: {
    name: 'CreateAccountModal',
    inheritAttrs: false,
    props: ['show', 'proxies', 'groups', 'initialPlatform', 'lockPlatform', 'requiredGroupId'],
    emits: ['close', 'created'],
    template: '<div v-if="show" data-testid="create-account-editor"><button data-testid="created" @click="$emit(\'created\')" /></div>'
  },
  EditAccountModal: {
    name: 'EditAccountModal',
    inheritAttrs: false,
    props: ['show', 'account', 'proxies', 'groups', 'preserveGroupMembership'],
    emits: ['close', 'updated'],
    template: '<div v-if="show" data-testid="edit-account-editor"><button data-testid="updated" @click="$emit(\'updated\', account)" /></div>'
  }
}))

const candidateOne: GroupAccountSummary = {
  id: 1,
  name: 'Candidate One',
  platform: 'openai',
  type: 'oauth',
  status: 'active',
  schedulable: true,
  rate_limited_at: null,
  rate_limit_reset_at: null,
  overload_until: null,
  temp_unschedulable_until: null,
  group_count: 1,
  policy_warnings: []
}

const candidateTwo: GroupAccountSummary = {
  ...candidateOne,
  id: 2,
  name: 'Candidate Two',
  type: 'apikey'
}

const member: GroupAccountSummary = {
  ...candidateOne,
  id: 3,
  name: 'Current Member',
  group_count: 2
}

const createdMember: GroupAccountSummary = {
  ...candidateOne,
  id: 4,
  name: 'Created in Group'
}

const memberTwo: GroupAccountSummary = {
  ...member,
  id: 5,
  name: 'Second Member'
}

const accountFromSummary = (
  summary: GroupAccountSummary,
  groupIds: number[] = [42]
): Account => ({
  ...summary,
  proxy_id: null,
  concurrency: 10,
  priority: 1,
  error_message: null,
  last_used_at: null,
  expires_at: null,
  auto_pause_on_expired: false,
  created_at: '2026-08-25T00:00:00Z',
  updated_at: '2026-08-25T00:00:00Z',
  group_ids: groupIds
})

const createdAccount = accountFromSummary(createdMember)

const group = {
  id: 42,
  name: 'Primary',
  platform: 'openai',
  status: 'active',
  account_count: 1,
  active_account_count: 1,
  rate_limited_account_count: 0
} as AdminGroup

const BaseDialogStub = defineComponent({
  name: 'BaseDialog',
  props: ['show', 'title'],
  emits: ['close'],
  template: '<div v-if="show" data-testid="base-dialog"><slot /><slot name="footer" /></div>'
})

const ConfirmDialogStub = defineComponent({
  name: 'ConfirmDialog',
  props: ['show', 'title', 'message'],
  emits: ['confirm', 'cancel'],
  template: '<div v-if="show" data-testid="confirm-dialog"><span>{{ title }}</span><button data-testid="confirm" @click="$emit(\'confirm\')" /><button data-testid="dismiss" @click="$emit(\'cancel\')" /></div>'
})

const PaginationStub = defineComponent({
  name: 'Pagination',
  inheritAttrs: false,
  props: ['page', 'total', 'pageSize'],
  emits: ['update:page', 'update:pageSize'],
  template: '<div v-bind="$attrs"><button data-testid="next-page" @click="$emit(\'update:page\', page + 1)" /></div>'
})

const SelectStub = defineComponent({
  name: 'SelectControl',
  props: ['modelValue', 'options'],
  emits: ['update:modelValue', 'change'],
  template: '<select :value="modelValue" />'
})

function mountModal(overrides: Partial<AdminGroup> = {}) {
  return mount(GroupAccountsModal, {
    props: {
      show: true,
      group: { ...group, ...overrides }
    },
    global: {
      stubs: {
        BaseDialog: BaseDialogStub,
        ConfirmDialog: ConfirmDialogStub,
        Pagination: PaginationStub,
        Select: SelectStub,
        PlatformIcon: true,
        Icon: true
      }
    }
  })
}

async function selectAndMove(wrapper: ReturnType<typeof mountModal>, rowId: number, direction: 'candidates' | 'members') {
  const row = wrapper.get(`[data-testid="${direction === 'candidates' ? 'candidate' : 'member'}-row-${rowId}"]`)
  await row.get('input[type="checkbox"]').setValue(true)
  await wrapper.get(`[data-testid="move-${direction}"]`).trigger('click')
}

describe('GroupAccountsModal', () => {
  beforeEach(() => {
    vi.spyOn(globalThis.crypto, 'randomUUID').mockReturnValue('11111111-1111-4111-8111-111111111111')
    for (const mock of [
      listAccounts,
      listAccountCandidates,
      updateAccounts,
      getAllGroups,
      getAllProxies,
      getAccountById,
      showSuccess,
      showError
    ]) {
      mock.mockReset()
    }
    listAccountCandidates.mockResolvedValue({
      items: [candidateOne, candidateTwo],
      total: 2,
      eligible_total: 2,
      page: 1,
      page_size: 20,
      pages: 1
    })
    listAccounts.mockResolvedValue({
      items: [member],
      total: 1,
      member_total: 1,
      page: 1,
      page_size: 20,
      pages: 1
    })
    updateAccounts.mockResolvedValue({
      added_account_ids: [1],
      removed_account_ids: [3],
      already_member_account_ids: [],
      not_member_account_ids: [],
      account_count: 1,
      active_account_count: 1,
      rate_limited_account_count: 0
    })
    getAllGroups.mockResolvedValue([group])
    getAllProxies.mockResolvedValue([])
    getAccountById.mockResolvedValue({ ...member, group_ids: [42] })
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('loads both server-paginated sides and pages them independently', async () => {
    const wrapper = mountModal()
    await flushPromises()

    expect(listAccountCandidates).toHaveBeenCalledWith(42, 1, 20, expect.any(Object), expect.any(Object))
    expect(listAccounts).toHaveBeenCalledWith(42, 1, 20, expect.any(Object), expect.any(Object))

    await wrapper.get('[data-testid="candidate-pagination"]').get('[data-testid="next-page"]').trigger('click')
    await flushPromises()

    expect(listAccountCandidates).toHaveBeenLastCalledWith(42, 2, 20, expect.any(Object), expect.any(Object))
    expect(listAccounts).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })

  it('closes when the backend returns the canonical uppercase missing-group code', async () => {
    listAccounts.mockRejectedValueOnce({ code: 'GROUP_NOT_FOUND', message: 'group not found' })
    const wrapper = mountModal()
    await flushPromises()

    expect(showError).toHaveBeenCalledWith('admin.groups.accountManagement.errors.group_not_found')
    expect(wrapper.emitted('close')).toHaveLength(1)
    wrapper.unmount()
  })

  it('uses reset deadlines instead of historical rate-limit timestamps for scheduling labels', async () => {
    listAccountCandidates.mockResolvedValueOnce({
      items: [{
        ...candidateOne,
        rate_limited_at: new Date(Date.now() - 60_000).toISOString(),
        rate_limit_reset_at: new Date(Date.now() - 1_000).toISOString()
      }],
      total: 1,
      eligible_total: 1,
      page: 1,
      page_size: 20,
      pages: 1
    })
    const wrapper = mountModal()
    await flushPromises()

    expect(wrapper.get('[data-testid="candidate-row-1"]').text()).toContain(
      'admin.groups.accountManagement.scheduling.schedulable'
    )
    expect(wrapper.get('[data-testid="candidate-row-1"]').text()).not.toContain(
      'admin.groups.accountManagement.scheduling.rateLimited'
    )
    wrapper.unmount()
  })

  it('submits one atomic diff for staged additions and removals', async () => {
    const wrapper = mountModal()
    await flushPromises()
    await selectAndMove(wrapper, 1, 'candidates')
    await selectAndMove(wrapper, 3, 'members')

    await wrapper.get('[data-testid="save-group-accounts"]').trigger('click')
    await flushPromises()

    expect(updateAccounts).toHaveBeenCalledWith(
      42,
      {
        add_account_ids: [1],
        remove_account_ids: [3],
        risk_confirmation_token: null
      },
      { idempotencyKey: expect.stringContaining('group-accounts-42-') }
    )
    expect(wrapper.emitted('success')).toHaveLength(1)
    expect(wrapper.emitted('close')).toHaveLength(1)
    wrapper.unmount()
  })

  it('treats moving a staged account back as an undo', async () => {
    const wrapper = mountModal()
    await flushPromises()
    await selectAndMove(wrapper, 1, 'candidates')
    await selectAndMove(wrapper, 1, 'members')

    expect(wrapper.get('[data-testid="save-group-accounts"]').attributes('disabled')).toBeDefined()
    expect(wrapper.find('[data-testid="member-row-1"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('retains the draft and reuses the idempotency key after a failed save', async () => {
    updateAccounts.mockRejectedValueOnce({ status: 0, message: 'timeout' })
    const wrapper = mountModal()
    await flushPromises()
    await selectAndMove(wrapper, 1, 'candidates')

    await wrapper.get('[data-testid="save-group-accounts"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="member-row-1"]').exists()).toBe(true)
    const firstKey = updateAccounts.mock.calls[0][2].idempotencyKey

    await wrapper.get('[data-testid="save-group-accounts"]').trigger('click')
    await flushPromises()
    expect(updateAccounts.mock.calls[1][2].idempotencyKey).toBe(firstKey)
    wrapper.unmount()
  })

  it('reuses the confirmed token and idempotency key after a retryable confirmation failure', async () => {
    vi.mocked(globalThis.crypto.randomUUID)
      .mockReturnValueOnce('11111111-1111-4111-8111-111111111111')
      .mockReturnValueOnce('22222222-2222-4222-8222-222222222222')
    updateAccounts
      .mockRejectedValueOnce({
        status: 409,
        reason: 'mixed_channel_warning',
        metadata: { risk_confirmation_token: 'confirmed-risk-token' }
      })
      .mockRejectedValueOnce({ status: 0, message: 'timeout' })

    const wrapper = mountModal()
    await flushPromises()
    await selectAndMove(wrapper, 1, 'candidates')

    await wrapper.get('[data-testid="save-group-accounts"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="confirm-dialog"]').get('[data-testid="confirm"]').trigger('click')
    await flushPromises()

    const confirmedKey = updateAccounts.mock.calls[1][2].idempotencyKey
    await wrapper.get('[data-testid="save-group-accounts"]').trigger('click')
    await flushPromises()

    expect(updateAccounts.mock.calls[2][1].risk_confirmation_token).toBe('confirmed-risk-token')
    expect(updateAccounts.mock.calls[2][2].idempotencyKey).toBe(confirmedKey)
    wrapper.unmount()
  })

  it('drops a confirmed token and operation key after a definitive client error', async () => {
    vi.mocked(globalThis.crypto.randomUUID)
      .mockReturnValueOnce('11111111-1111-4111-8111-111111111111')
      .mockReturnValueOnce('22222222-2222-4222-8222-222222222222')
      .mockReturnValueOnce('33333333-3333-4333-8333-333333333333')
    updateAccounts
      .mockRejectedValueOnce({
        status: 409,
        reason: 'mixed_channel_warning',
        metadata: { risk_confirmation_token: 'stale-risk-token' }
      })
      .mockRejectedValueOnce({ status: 422, reason: 'account_group_policy_violation' })

    const wrapper = mountModal()
    await flushPromises()
    await selectAndMove(wrapper, 1, 'candidates')

    await wrapper.get('[data-testid="save-group-accounts"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="confirm-dialog"]').get('[data-testid="confirm"]').trigger('click')
    await flushPromises()

    const rejectedKey = updateAccounts.mock.calls[1][2].idempotencyKey
    await wrapper.get('[data-testid="save-group-accounts"]').trigger('click')
    await flushPromises()

    expect(updateAccounts.mock.calls[2][1].risk_confirmation_token).toBeNull()
    expect(updateAccounts.mock.calls[2][2].idempotencyKey).not.toBe(rejectedKey)
    wrapper.unmount()
  })

  it('blocks saving until the initial member baseline succeeds and while its retryable error is unresolved', async () => {
    let resolveInitialMembers!: (value: unknown) => void
    listAccounts.mockImplementationOnce(() => new Promise((resolve) => {
      resolveInitialMembers = resolve
    }))
    const wrapper = mountModal()
    await flushPromises()
    await selectAndMove(wrapper, 1, 'candidates')

    const saveButton = wrapper.get('[data-testid="save-group-accounts"]')
    expect(saveButton.attributes('disabled')).toBeDefined()
    await saveButton.trigger('click')
    expect(updateAccounts).not.toHaveBeenCalled()

    resolveInitialMembers({
      items: [member],
      total: 1,
      member_total: 1,
      page: 1,
      page_size: 20,
      pages: 1
    })
    await flushPromises()
    expect(saveButton.attributes('disabled')).toBeUndefined()

    listAccounts.mockRejectedValueOnce({ message: 'members unavailable' })
    await wrapper.get('[data-testid="member-pagination"]').get('[data-testid="next-page"]').trigger('click')
    await flushPromises()
    expect(saveButton.attributes('disabled')).toBeDefined()
    await saveButton.trigger('click')
    expect(updateAccounts).not.toHaveBeenCalled()

    await wrapper.get('[data-testid="retry-members"]').trigger('click')
    await flushPromises()
    expect(saveButton.attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })

  it('uses the server risk token and a new key only after confirmation', async () => {
    vi.mocked(globalThis.crypto.randomUUID)
      .mockReturnValueOnce('11111111-1111-4111-8111-111111111111')
      .mockReturnValueOnce('22222222-2222-4222-8222-222222222222')
    updateAccounts.mockRejectedValueOnce({
      status: 409,
      reason: 'mixed_channel_warning',
      metadata: { group_id: '42', risk_confirmation_token: 'risk-token' }
    })
    const wrapper = mountModal()
    await flushPromises()
    await selectAndMove(wrapper, 1, 'candidates')

    await wrapper.get('[data-testid="save-group-accounts"]').trigger('click')
    await flushPromises()
    const firstKey = updateAccounts.mock.calls[0][2].idempotencyKey
    await wrapper.get('[data-testid="confirm-dialog"]').get('[data-testid="confirm"]').trigger('click')
    await flushPromises()

    expect(updateAccounts.mock.calls[1][1].risk_confirmation_token).toBe('risk-token')
    expect(updateAccounts.mock.calls[1][2].idempotencyKey).not.toBe(firstKey)
    wrapper.unmount()
  })

  it('asks before discarding a non-empty draft', async () => {
    const wrapper = mountModal()
    await flushPromises()
    await selectAndMove(wrapper, 1, 'candidates')

    await wrapper.get('[data-testid="group-accounts-cancel"]').trigger('click')
    expect(wrapper.find('[data-testid="confirm-dialog"]').exists()).toBe(true)
    expect(wrapper.emitted('close')).toBeUndefined()

    await wrapper.get('[data-testid="confirm-dialog"]').get('[data-testid="confirm"]').trigger('click')
    expect(wrapper.emitted('close')).toHaveLength(1)
    wrapper.unmount()
  })

  it('clears all transient selections on the side after a single-row move', async () => {
    listAccounts.mockResolvedValueOnce({
      items: [member, memberTwo],
      total: 2,
      member_total: 2,
      page: 1,
      page_size: 20,
      pages: 1
    })
    const wrapper = mountModal()
    await flushPromises()

    await wrapper.get('[data-testid="candidate-row-1"] input[type="checkbox"]').setValue(true)
    await wrapper.get('[data-testid="candidate-row-2"] input[type="checkbox"]').setValue(true)
    await wrapper.get('[data-testid="add-account-1"]').trigger('click')

    expect(wrapper.get('[data-testid="move-candidates"]').attributes('disabled')).toBeDefined()
    expect((wrapper.get('[data-testid="candidate-row-2"] input[type="checkbox"]').element as HTMLInputElement).checked).toBe(false)

    await wrapper.get('[data-testid="member-row-3"] input[type="checkbox"]').setValue(true)
    await wrapper.get('[data-testid="member-row-5"] input[type="checkbox"]').setValue(true)
    await wrapper.get('[data-testid="remove-account-3"]').trigger('click')

    expect(wrapper.get('[data-testid="move-members"]').attributes('disabled')).toBeDefined()
    expect((wrapper.get('[data-testid="member-row-5"] input[type="checkbox"]').element as HTMLInputElement).checked).toBe(false)
    wrapper.unmount()
  })

  it('pins a created account even when the refreshed member page does not contain it', async () => {
    listAccounts
      .mockResolvedValueOnce({
        items: [member],
        total: 1,
        member_total: 1,
        page: 2,
        page_size: 20,
        pages: 2
      })
      .mockResolvedValueOnce({
        items: [member],
        total: 1,
        member_total: 2,
        page: 2,
        page_size: 20,
        pages: 2
      })
    const wrapper = mountModal()
    await flushPromises()
    await selectAndMove(wrapper, 1, 'candidates')

    await wrapper.get('[data-testid="create-group-account"]').trigger('click')
    await flushPromises()
    wrapper.getComponent({ name: 'CreateAccountModal' }).vm.$emit('created', createdAccount)
    await flushPromises()

    expect(wrapper.get('[data-testid="member-row-4"]').text()).toContain('Created in Group')
    expect(wrapper.get('[data-testid="member-row-1"]').text()).toContain('Candidate One')
    wrapper.unmount()
  })

  it('refreshes a pending-add summary after editing the account', async () => {
    const renamedAccount = accountFromSummary(
      { ...candidateOne, name: 'Renamed Candidate' },
      [7]
    )
    getAccountById.mockResolvedValueOnce(renamedAccount)
    const wrapper = mountModal()
    await flushPromises()
    await selectAndMove(wrapper, 1, 'candidates')

    const pendingRow = wrapper.get('[data-testid="member-row-1"]')
    await pendingRow.findAll('button')[0].trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="updated"]').trigger('click')
    await flushPromises()

    expect(wrapper.get('[data-testid="member-row-1"]').text()).toContain('Renamed Candidate')
    expect(wrapper.get('[data-testid="save-group-accounts"]').attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })

  it('opens create and edit in group context, then refreshes without clearing the draft', async () => {
    listAccounts
      .mockResolvedValueOnce({
        items: [member],
        total: 1,
        member_total: 1,
        page: 1,
        page_size: 20,
        pages: 1
      })
      .mockResolvedValueOnce({
        items: [createdMember, candidateOne, member],
        total: 3,
        member_total: 3,
        page: 1,
        page_size: 20,
        pages: 1
      })
    const wrapper = mountModal()
    await flushPromises()
    await selectAndMove(wrapper, 1, 'candidates')

    await wrapper.get('[data-testid="create-group-account"]').trigger('click')
    await flushPromises()
    const createEditor = wrapper.getComponent({ name: 'CreateAccountModal' })
    expect(createEditor.props()).toMatchObject({
      initialPlatform: 'openai',
      lockPlatform: true,
      requiredGroupId: 42
    })
    await wrapper.get('[data-testid="created"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="member-row-1"]').exists()).toBe(true)
    expect(wrapper.findAll('[data-testid="member-row-1"]')).toHaveLength(1)
    expect(wrapper.find('[data-testid="member-row-4"]').exists()).toBe(true)

    await wrapper.get('[data-testid="edit-account-3"]').trigger('click')
    await flushPromises()
    const editEditor = wrapper.getComponent({ name: 'EditAccountModal' })
    expect(getAccountById).toHaveBeenCalledWith(3)
    expect(editEditor.props('preserveGroupMembership')).toBe(true)
    wrapper.unmount()
  })

  it('leaves the account platform selectable for composite groups', async () => {
    const wrapper = mountModal({ platform: 'composite' })
    await flushPromises()
    await wrapper.get('[data-testid="create-group-account"]').trigger('click')
    await flushPromises()

    expect(wrapper.getComponent({ name: 'CreateAccountModal' }).props()).toMatchObject({
      initialPlatform: undefined,
      lockPlatform: false,
      requiredGroupId: 42
    })
    wrapper.unmount()
  })
})
