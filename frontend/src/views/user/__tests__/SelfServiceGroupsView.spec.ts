import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { nextTick } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import type { SelfServiceGroup, SelfServiceGroupPlatform } from '@/types'

const {
  listGroups,
  getGroup,
  listPlatforms,
  createGroup,
  updateGroup,
  deleteGroup,
  showSuccess,
  showError,
} = vi.hoisted(() => ({
  listGroups: vi.fn(),
  getGroup: vi.fn(),
  listPlatforms: vi.fn(),
  createGroup: vi.fn(),
  updateGroup: vi.fn(),
  deleteGroup: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn(),
}))

vi.mock('@/api', () => ({
  selfServiceGroupsAPI: {
    list: listGroups,
    getById: getGroup,
    listPlatforms,
    create: createGroup,
    update: updateGroup,
    delete: deleteGroup,
  },
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showSuccess,
    showError,
  }),
}))

const messages: Record<string, string> = {
  'common.cancel': 'Cancel',
  'common.close': 'Close',
  'common.delete': 'Delete',
  'common.edit': 'Edit',
  'common.loading': 'Loading',
  'common.retry': 'Retry',
  'common.search': 'Search',
  'selfServiceGroups.catalog.emptyDescription': 'No platforms have been enabled.',
  'selfServiceGroups.catalog.emptyTitle': 'No platforms are available to add',
  'selfServiceGroups.catalog.errorDescription': 'Catalog unavailable.',
  'selfServiceGroups.catalog.errorTitle': 'The platform catalog could not be loaded',
  'selfServiceGroups.catalog.forbiddenDescription': 'Hosting access is required.',
  'selfServiceGroups.catalog.forbiddenTitle': 'Group hosting is not available for this user',
  'selfServiceGroups.catalog.retry': 'Retry catalog',
  'selfServiceGroups.create.description': 'Description',
  'selfServiceGroups.create.descriptionCount': '{count}/2000',
  'selfServiceGroups.create.descriptionPlaceholder': 'Optional description',
  'selfServiceGroups.create.name': 'Group name',
  'selfServiceGroups.create.namePlaceholder': 'Group name',
  'selfServiceGroups.create.platform': 'Platform',
  'selfServiceGroups.create.platformDescription': 'Choose a server platform.',
  'selfServiceGroups.create.submit': 'Create group',
  'selfServiceGroups.create.submitting': 'Creating',
  'selfServiceGroups.create.success': 'Group created',
  'selfServiceGroups.create.title': 'Add private group',
  'selfServiceGroups.createGroup': 'Add group',
  'selfServiceGroups.delete.confirm': 'Delete',
  'selfServiceGroups.delete.deleting': 'Deleting',
  'selfServiceGroups.delete.message': 'Delete {name}?',
  'selfServiceGroups.delete.success': 'Group deleted',
  'selfServiceGroups.delete.title': 'Delete group',
  'selfServiceGroups.detail.createdAt': 'Created',
  'selfServiceGroups.detail.description': 'Description',
  'selfServiceGroups.detail.descriptionPlaceholder': 'Optional description',
  'selfServiceGroups.detail.id': 'Group ID',
  'selfServiceGroups.detail.loading': 'Loading details',
  'selfServiceGroups.detail.managedFields': 'Pricing and routing are operations-managed.',
  'selfServiceGroups.detail.name': 'Group name',
  'selfServiceGroups.detail.noDescription': 'No description',
  'selfServiceGroups.detail.platform': 'Platform',
  'selfServiceGroups.detail.readOnly': 'Read only',
  'selfServiceGroups.detail.retry': 'Retry',
  'selfServiceGroups.detail.save': 'Save changes',
  'selfServiceGroups.detail.saveSuccess': 'Group details updated',
  'selfServiceGroups.detail.saving': 'Saving',
  'selfServiceGroups.detail.status': 'Status',
  'selfServiceGroups.detail.title': 'Group details',
  'selfServiceGroups.detail.updatedAt': 'Updated',
  'selfServiceGroups.empty.description': 'No groups yet.',
  'selfServiceGroups.empty.title': 'No hosted groups yet',
  'selfServiceGroups.errors.create': 'Create failed',
  'selfServiceGroups.errors.delete': 'Delete failed',
  'selfServiceGroups.errors.invalidDescription': 'Description is too long',
  'selfServiceGroups.errors.invalidName': 'Invalid group name',
  'selfServiceGroups.errors.loadDetail': 'Group detail failed',
  'selfServiceGroups.errors.loadGroups': 'Group list failed',
  'selfServiceGroups.errors.update': 'Update failed',
  'selfServiceGroups.refresh': 'Refresh',
  'selfServiceGroups.status.active': 'Active',
  'selfServiceGroups.status.unknown': 'Unknown',
}

vi.mock('vue-i18n', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-i18n')>()
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) => {
        let value = messages[key] ?? key
        for (const [name, replacement] of Object.entries(params ?? {})) {
          value = value.replace(`{${name}}`, String(replacement))
        }
        return value
      },
      te: (key: string) => key in messages,
    }),
  }
})

import SelfServiceGroupsView from '../SelfServiceGroupsView.vue'

const platform: SelfServiceGroupPlatform = {
  id: 'openai',
  name: 'OpenAI',
  platform: 'openai',
}

const group: SelfServiceGroup = {
  id: 20,
  name: 'Personal OpenAI',
  description: 'Private routing group',
  platform: 'openai',
  status: 'active',
  owned_by_me: true,
  public_access_level: null,
  created_at: '2026-09-01T08:00:00Z',
  updated_at: '2026-09-01T09:00:00Z',
}

const AppLayoutStub = { template: '<main><slot /></main>' }
const TablePageLayoutStub = {
  template: `
    <section>
      <slot name="filters" />
      <slot name="table" />
      <slot name="pagination" />
    </section>
  `,
}
const DataTableStub = {
  name: 'DataTable',
  props: ['data', 'loading'],
  emits: ['row-click', 'sort'],
  template: `
    <div data-test="data-table">
      <span v-if="loading">Loading rows</span>
      <template v-else-if="data.length">
        <button
          v-for="row in data"
          :key="row.id"
          type="button"
          :data-test="'group-row-' + row.id"
          @click="$emit('row-click', row)"
        >
          {{ row.name }}
        </button>
      </template>
      <slot v-else name="empty" />
    </div>
  `,
}
const EmptyStateStub = {
  props: ['title', 'description', 'actionText'],
  emits: ['action'],
  template: `
    <div data-test="empty-state">
      <span>{{ title }}</span>
      <span>{{ description }}</span>
      <button v-if="actionText" type="button" @click="$emit('action')">{{ actionText }}</button>
    </div>
  `,
}
const BaseDialogStub = {
  props: ['show', 'title'],
  emits: ['close'],
  template: `
    <section v-if="show" data-test="base-dialog" :data-title="title">
      <h2>{{ title }}</h2>
      <slot />
      <slot name="footer" />
    </section>
  `,
}
const InputStub = {
  name: 'Input',
  props: ['modelValue', 'label', 'type', 'error'],
  emits: ['update:modelValue', 'enter'],
  template: `
    <label>
      <span>{{ label }}</span>
      <input
        :aria-label="label"
        :type="type || 'text'"
        :value="modelValue"
        @input="$emit('update:modelValue', $event.target.value)"
        @keyup.enter="$emit('enter')"
      />
      <span v-if="error">{{ error }}</span>
    </label>
  `,
}
const ConfirmDialogStub = {
  props: ['show', 'title', 'message', 'confirmText'],
  emits: ['confirm', 'cancel'],
  template: `
    <section v-if="show" data-test="confirm-dialog">
      <h2>{{ title }}</h2>
      <p>{{ message }}</p>
      <button type="button" data-test="confirm-delete" @click="$emit('confirm')">{{ confirmText }}</button>
      <button type="button" @click="$emit('cancel')">Cancel</button>
    </section>
  `,
}

function findButton(wrapper: VueWrapper, text: string) {
  const button = wrapper.findAll('button').find((candidate) => candidate.text() === text)
  if (!button) throw new Error(`Button not found: ${text}`)
  return button
}

async function mountView(): Promise<VueWrapper> {
  const wrapper = mount(SelfServiceGroupsView, {
    global: {
      stubs: {
        AppLayout: AppLayoutStub,
        TablePageLayout: TablePageLayoutStub,
        DataTable: DataTableStub,
        EmptyState: EmptyStateStub,
        BaseDialog: BaseDialogStub,
        ConfirmDialog: ConfirmDialogStub,
        Input: InputStub,
        Pagination: true,
        StatusBadge: true,
        Icon: true,
      },
    },
  })
  await flushPromises()
  await nextTick()
  return wrapper
}

describe('SelfServiceGroupsView', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    listGroups.mockResolvedValue({
      items: [group],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    listPlatforms.mockResolvedValue([platform])
    getGroup.mockResolvedValue(group)
    createGroup.mockResolvedValue({ ...group, id: 21 })
    updateGroup.mockResolvedValue({ ...group, description: 'Updated description' })
    deleteGroup.mockResolvedValue(group)
  })

  it('disables creation when the platform catalog is empty', async () => {
    listPlatforms.mockResolvedValue([])
    const wrapper = await mountView()

    expect(wrapper.get('[data-test="group-catalog-notice"]').text()).toContain(
      'No platforms are available to add'
    )
    expect(findButton(wrapper, 'Add group').attributes('disabled')).toBeDefined()
  })

  it('shows the no-eligibility state when the catalog endpoint denies access', async () => {
    listPlatforms.mockRejectedValue({ reason: 'SELF_SERVICE_GROUP_FORBIDDEN' })
    const wrapper = await mountView()

    expect(wrapper.get('[data-test="group-catalog-notice"]').text()).toContain(
      'Group hosting is not available for this user'
    )
    expect(findButton(wrapper, 'Add group').attributes('disabled')).toBeDefined()
  })

  it('shows a retry action when the platform catalog fails unexpectedly', async () => {
    listPlatforms.mockRejectedValue(new Error('offline'))
    const wrapper = await mountView()

    expect(wrapper.get('[data-test="group-catalog-notice"]').text()).toContain(
      'The platform catalog could not be loaded'
    )
    await findButton(wrapper, 'Retry catalog').trigger('click')
    expect(listPlatforms).toHaveBeenCalledTimes(2)
  })

  it('creates from the server catalog with an exact restricted payload', async () => {
    const wrapper = await mountView()

    await findButton(wrapper, 'Add group').trigger('click')
    await wrapper.get('[data-test="group-platform-openai"]').trigger('click')
    await wrapper.get('input[aria-label="Group name"]').setValue('  New group  ')
    await wrapper.get('#self-service-group-create-description').setValue('  Private route  ')
    await findButton(wrapper, 'Create group').trigger('click')
    await flushPromises()

    expect(createGroup).toHaveBeenCalledWith({
      name: 'New group',
      description: 'Private route',
      platform_id: 'openai',
    })
    expect(showSuccess).toHaveBeenCalledWith('Group created')
  })

  it('loads only the safe group detail projection when a row is opened', async () => {
    const wrapper = await mountView()

    await wrapper.get('[data-test="group-row-20"]').trigger('click')
    await flushPromises()

    expect(getGroup).toHaveBeenCalledWith(20)
    const dialog = wrapper.get('[data-title="Group details"]')
    expect(dialog.get('input[aria-label="Group name"]').element).toHaveProperty(
      'value',
      'Personal OpenAI'
    )
    expect(dialog.text()).toContain('OpenAI')
    expect(dialog.text()).toContain('Pricing and routing are operations-managed.')
    expect(dialog.text()).not.toContain('rate_multiplier')
    expect(dialog.text()).not.toContain('account_count')
  })

  it('updates an owned group with only the changed mutable field', async () => {
    const wrapper = await mountView()

    await wrapper.get('[data-test="group-row-20"]').trigger('click')
    await flushPromises()
    await wrapper.get('#self-service-group-detail-description').setValue('  Updated description  ')
    await findButton(wrapper, 'Save changes').trigger('click')
    await flushPromises()

    expect(updateGroup).toHaveBeenCalledWith(20, { description: 'Updated description' })
    expect(showSuccess).toHaveBeenCalledWith('Group details updated')
  })

  it('keeps shared groups read-only', async () => {
    const sharedGroup = { ...group, id: 22, owned_by_me: false }
    listGroups.mockResolvedValue({ items: [sharedGroup], total: 1, page: 1, page_size: 20, pages: 1 })
    getGroup.mockResolvedValue(sharedGroup)
    const wrapper = await mountView()

    await wrapper.get('[data-test="group-row-22"]').trigger('click')
    await flushPromises()

    const dialog = wrapper.get('[data-title="Group details"]')
    expect(dialog.text()).toContain('Read only')
    expect(dialog.find('input[aria-label="Group name"]').exists()).toBe(false)
    expect(dialog.text()).not.toContain('Save changes')
  })

  it('deletes an owned group only after confirmation and reloads the list', async () => {
    const wrapper = await mountView()

    await wrapper.get('[data-test="group-row-20"]').trigger('click')
    await flushPromises()
    await findButton(wrapper, 'Delete').trigger('click')

    expect(deleteGroup).not.toHaveBeenCalled()
    expect(wrapper.get('[data-test="confirm-dialog"]').text()).toContain('Personal OpenAI')

    await wrapper.get('[data-test="confirm-delete"]').trigger('click')
    await flushPromises()

    expect(deleteGroup).toHaveBeenCalledWith(20)
    expect(showSuccess).toHaveBeenCalledWith('Group deleted')
    expect(listGroups).toHaveBeenCalledTimes(2)
  })
})
