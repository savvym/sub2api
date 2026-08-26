import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { AdminGroup } from '@/types'
import GroupsView from '@/views/admin/GroupsView.vue'

const {
  listGroups,
  getUsageSummary,
  getCapacitySummary,
  getLiveCapability,
  createGroup,
  updateGroup
} = vi.hoisted(() => ({
  listGroups: vi.fn(),
  getUsageSummary: vi.fn(),
  getCapacitySummary: vi.fn(),
  getLiveCapability: vi.fn(),
  createGroup: vi.fn(),
  updateGroup: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    groups: {
      list: listGroups,
      getUsageSummary,
      getCapacitySummary,
      getLiveCapability,
      getModelsListCandidates: vi.fn().mockResolvedValue([]),
      getAll: vi.fn().mockResolvedValue([]),
      duplicate: vi.fn(),
      create: createGroup,
      update: updateGroup,
      delete: vi.fn(),
      updateSortOrder: vi.fn()
    },
    accounts: { list: vi.fn(), getById: vi.fn() }
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showSuccess: vi.fn(), showError: vi.fn() })
}))

vi.mock('@/stores/onboarding', () => ({
  useOnboardingStore: () => ({ isCurrentStep: vi.fn(() => false), nextStep: vi.fn() })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

const group = {
  id: 42,
  name: 'Primary',
  platform: 'anthropic',
  status: 'active',
  subscription_type: 'standard',
  rate_multiplier: 1,
  is_exclusive: false,
  account_count: 3,
  active_account_count: 2,
  rate_limited_account_count: 1,
  sort_order: 1
} as AdminGroup

const DataTableStub = defineComponent({
  props: { data: { type: Array, default: () => [] } },
  template: '<div><div v-for="row in data" :key="row.id"><slot name="cell-account_count" :row="row" /><slot name="cell-actions" :row="row" /></div></div>'
})

const GroupAccountsModalStub = defineComponent({
  name: 'GroupAccountsModal',
  props: ['show', 'group'],
  emits: ['close', 'success', 'refresh'],
  template: '<div v-if="show" data-testid="accounts-manager"><span>{{ group.name }}</span><button data-testid="manager-close" @click="$emit(\'close\')" /><button data-testid="manager-success" @click="$emit(\'success\', { account_count: 4, active_account_count: 3, rate_limited_account_count: 0 })" /></div>'
})

const BaseDialogStub = defineComponent({
  name: 'BaseDialog',
  props: { show: { type: Boolean, default: false } },
  template: '<div v-if="show"><slot /><slot name="footer" /></div>'
})

const ReasoningEffortPolicyFieldsStub = defineComponent({
  name: 'ReasoningEffortPolicyFields',
  setup(_, { expose }) {
    expose({ validate: () => true, resetValidation: () => undefined })
    return {}
  },
  template: '<div />'
})

function mountView() {
  return mount(GroupsView, {
    global: {
      stubs: {
        AppLayout: { template: '<main><slot /></main>' },
        TablePageLayout: { template: '<section><slot name="filters" /><slot name="table" /><slot name="pagination" /></section>' },
        DataTable: DataTableStub,
        GroupAccountsModal: GroupAccountsModalStub,
        Pagination: true,
        BaseDialog: BaseDialogStub,
        ConfirmDialog: true,
        EmptyState: true,
        Select: true,
        PlatformIcon: true,
        Icon: true,
        GroupCapacityBadge: true,
        GroupRateMultipliersModal: true,
        GroupRPMOverridesModal: true,
        ReasoningEffortPolicyFields: ReasoningEffortPolicyFieldsStub,
        VueDraggable: true
      }
    }
  })
}

describe('GroupsView account manager entry points', () => {
  beforeEach(() => {
    localStorage.clear()
    listGroups.mockReset().mockResolvedValue({
      items: [{ ...group }],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1
    })
    getUsageSummary.mockReset().mockResolvedValue([])
    getCapacitySummary.mockReset().mockResolvedValue([])
    getLiveCapability.mockReset().mockResolvedValue({ supported: true })
    createGroup.mockReset().mockResolvedValue({ ...group })
    updateGroup.mockReset().mockResolvedValue({ ...group })
  })

  it('opens from the count or row action and refreshes counts after saving', async () => {
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-testid="group-account-count"]').trigger('click')
    expect(wrapper.get('[data-testid="accounts-manager"]').text()).toContain('Primary')

    await wrapper.get('[data-testid="manager-success"]').trigger('click')
    await flushPromises()
    expect(listGroups).toHaveBeenCalledTimes(2)

    await wrapper.get('[data-testid="manager-close"]').trigger('click')
    expect(wrapper.find('[data-testid="accounts-manager"]').exists()).toBe(false)
    await wrapper.get('[data-testid="group-manage-accounts"]').trigger('click')
    expect(wrapper.find('[data-testid="accounts-manager"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('keeps account copying available when creating a group', async () => {
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-tour="groups-create-btn"]').trigger('click')
    await flushPromises()

    const form = wrapper.get('#create-group-form')
    expect(form.text()).toContain('admin.groups.copyAccounts.title')

    const sourceGroupSelect = form
      .findAll('select')
      .find((select) => select.text().includes('Primary'))
    expect(sourceGroupSelect).toBeTruthy()
    await sourceGroupSelect!.setValue('42')
    await form.get('[data-tour="group-form-name"]').setValue('Copied accounts')
    await form.trigger('submit')
    await flushPromises()

    expect(createGroup).toHaveBeenCalledTimes(1)
    expect(createGroup.mock.calls[0][0]).toMatchObject({
      copy_accounts_from_group_ids: [42]
    })
    wrapper.unmount()
  })

  it('removes account copying from edit and omits the legacy update field', async () => {
    const wrapper = mountView()
    await flushPromises()

    const editButton = wrapper
      .findAll('button')
      .find((button) => button.text() === 'common.edit')
    expect(editButton).toBeTruthy()
    await editButton!.trigger('click')
    await flushPromises()

    const form = wrapper.get('#edit-group-form')
    expect(form.text()).not.toContain('admin.groups.copyAccounts.title')
    await form.trigger('submit')
    await flushPromises()

    expect(updateGroup).toHaveBeenCalledTimes(1)
    expect(updateGroup.mock.calls[0][0]).toBe(42)
    expect(updateGroup.mock.calls[0][1]).not.toHaveProperty(
      'copy_accounts_from_group_ids'
    )
    wrapper.unmount()
  })
})
