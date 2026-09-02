import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { nextTick } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import type { SelfServiceAccount, SelfServiceAccountProduct } from '@/types'

const {
  listAccounts,
  getAccount,
  listProducts,
  createAccount,
  renameAccount,
  deleteAccount,
  showSuccess,
  showError,
} = vi.hoisted(() => ({
  listAccounts: vi.fn(),
  getAccount: vi.fn(),
  listProducts: vi.fn(),
  createAccount: vi.fn(),
  renameAccount: vi.fn(),
  deleteAccount: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn(),
}))

vi.mock('@/api', () => ({
  selfServiceAccountsAPI: {
    list: listAccounts,
    getById: getAccount,
    listProducts,
    create: createAccount,
    rename: renameAccount,
    delete: deleteAccount,
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
  'common.hide': 'Hide',
  'common.loading': 'Loading',
  'common.retry': 'Retry',
  'common.search': 'Search',
  'common.show': 'Show',
  'selfServiceAccounts.catalog.emptyDescription': 'No products have been enabled.',
  'selfServiceAccounts.catalog.emptyTitle': 'No products are available to add',
  'selfServiceAccounts.catalog.errorDescription': 'Catalog unavailable.',
  'selfServiceAccounts.catalog.errorTitle': 'The product catalog could not be loaded',
  'selfServiceAccounts.catalog.forbiddenDescription': 'Hosting access is required.',
  'selfServiceAccounts.catalog.forbiddenTitle': 'Hosting is not available for this user',
  'selfServiceAccounts.catalog.retry': 'Retry catalog',
  'selfServiceAccounts.create.apiKey': 'API Key',
  'selfServiceAccounts.create.apiKeyHint': 'Submitted once.',
  'selfServiceAccounts.create.apiKeyPlaceholder': 'Enter API key',
  'selfServiceAccounts.create.back': 'Back',
  'selfServiceAccounts.create.continue': 'Continue',
  'selfServiceAccounts.create.detailsStep': 'Enter credential',
  'selfServiceAccounts.create.name': 'Account name',
  'selfServiceAccounts.create.namePlaceholder': 'Account name',
  'selfServiceAccounts.create.productDescription': 'Choose a server product.',
  'selfServiceAccounts.create.productStep': 'Choose product',
  'selfServiceAccounts.create.selectedProduct': 'Selected product',
  'selfServiceAccounts.create.submit': 'Create account',
  'selfServiceAccounts.create.submitting': 'Creating',
  'selfServiceAccounts.create.success': 'Account created',
  'selfServiceAccounts.create.title': 'Add hosted account',
  'selfServiceAccounts.createAccount': 'Add account',
  'selfServiceAccounts.credential.configured': 'Configured',
  'selfServiceAccounts.credential.missing': 'Missing',
  'selfServiceAccounts.delete.confirm': 'Delete account',
  'selfServiceAccounts.delete.deleting': 'Deleting',
  'selfServiceAccounts.delete.message': 'Delete {name}?',
  'selfServiceAccounts.delete.success': 'Account deleted',
  'selfServiceAccounts.delete.title': 'Delete account',
  'selfServiceAccounts.detail.createdAt': 'Created',
  'selfServiceAccounts.detail.credential': 'Credential',
  'selfServiceAccounts.detail.id': 'Account ID',
  'selfServiceAccounts.detail.loading': 'Loading details',
  'selfServiceAccounts.detail.name': 'Account name',
  'selfServiceAccounts.detail.platform': 'Platform',
  'selfServiceAccounts.detail.readOnly': 'Read only',
  'selfServiceAccounts.detail.retry': 'Retry',
  'selfServiceAccounts.detail.save': 'Save name',
  'selfServiceAccounts.detail.saveSuccess': 'Name saved',
  'selfServiceAccounts.detail.saving': 'Saving',
  'selfServiceAccounts.detail.status': 'Status',
  'selfServiceAccounts.detail.title': 'Account details',
  'selfServiceAccounts.detail.type': 'Authentication',
  'selfServiceAccounts.detail.updatedAt': 'Updated',
  'selfServiceAccounts.empty.description': 'No accounts yet.',
  'selfServiceAccounts.empty.title': 'No hosted accounts yet',
  'selfServiceAccounts.errors.create': 'Create failed',
  'selfServiceAccounts.errors.delete': 'Delete failed',
  'selfServiceAccounts.errors.invalidApiKey': 'Invalid API key',
  'selfServiceAccounts.errors.invalidName': 'Invalid account name',
  'selfServiceAccounts.errors.loadAccounts': 'Account list failed',
  'selfServiceAccounts.errors.loadDetail': 'Account detail failed',
  'selfServiceAccounts.errors.rename': 'Rename failed',
  'selfServiceAccounts.refresh': 'Refresh',
  'selfServiceAccounts.status.active': 'Active',
  'selfServiceAccounts.status.unknown': 'Unknown',
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

import SelfServiceAccountsView from '../SelfServiceAccountsView.vue'

const product: SelfServiceAccountProduct = {
  id: 'openai-api-key',
  name: 'OpenAI API Key',
  platform: 'openai',
  type: 'apikey',
}

const account: SelfServiceAccount = {
  id: 10,
  name: 'Personal OpenAI',
  platform: 'openai',
  type: 'apikey',
  status: 'active',
  credential_configured: true,
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
          :data-test="'account-row-' + row.id"
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
      <slot name="suffix" />
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
  const wrapper = mount(SelfServiceAccountsView, {
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

describe('SelfServiceAccountsView', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    listAccounts.mockResolvedValue({
      items: [account],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    listProducts.mockResolvedValue([product])
    getAccount.mockResolvedValue(account)
    createAccount.mockResolvedValue({ ...account, id: 11 })
    renameAccount.mockResolvedValue({ ...account, name: 'Renamed account' })
    deleteAccount.mockResolvedValue(account)
  })

  it('disables creation when the product catalog is empty', async () => {
    listProducts.mockResolvedValue([])
    const wrapper = await mountView()

    expect(wrapper.get('[data-test="catalog-notice"]').text()).toContain(
      'No products are available to add'
    )
    expect(findButton(wrapper, 'Add account').attributes('disabled')).toBeDefined()
  })

  it('shows the no-eligibility state when the catalog endpoint denies access', async () => {
    listProducts.mockRejectedValue({ reason: 'SELF_SERVICE_ACCOUNT_FORBIDDEN' })
    const wrapper = await mountView()

    expect(wrapper.get('[data-test="catalog-notice"]').text()).toContain(
      'Hosting is not available for this user'
    )
    expect(findButton(wrapper, 'Add account').attributes('disabled')).toBeDefined()
  })

  it('shows a retry action when the product catalog fails unexpectedly', async () => {
    listProducts.mockRejectedValue(new Error('offline'))
    const wrapper = await mountView()

    expect(wrapper.get('[data-test="catalog-notice"]').text()).toContain(
      'The product catalog could not be loaded'
    )
    await findButton(wrapper, 'Retry catalog').trigger('click')
    expect(listProducts).toHaveBeenCalledTimes(2)
  })

  it('creates from the server catalog with an exact payload and clears the API key', async () => {
    const wrapper = await mountView()

    await findButton(wrapper, 'Add account').trigger('click')
    await wrapper.get('[data-test="product-openai-api-key"]').trigger('click')
    await findButton(wrapper, 'Continue').trigger('click')
    await wrapper.get('input[aria-label="Account name"]').setValue('  New account  ')
    await wrapper.get('input[aria-label="API Key"]').setValue('  sk-secret  ')
    await findButton(wrapper, 'Create account').trigger('click')
    await flushPromises()

    expect(createAccount).toHaveBeenCalledWith({
      name: 'New account',
      product_id: 'openai-api-key',
      api_key: 'sk-secret',
    })
    expect(showSuccess).toHaveBeenCalledWith('Account created')

    await findButton(wrapper, 'Add account').trigger('click')
    await findButton(wrapper, 'Continue').trigger('click')
    expect(wrapper.get('input[aria-label="API Key"]').element).toHaveProperty('value', '')
  })

  it('loads the safe account detail projection when a row is opened', async () => {
    const wrapper = await mountView()

    await wrapper.get('[data-test="account-row-10"]').trigger('click')
    await flushPromises()

    expect(getAccount).toHaveBeenCalledWith(10)
    const dialog = wrapper.get('[data-title="Account details"]')
    expect(dialog.get('input[aria-label="Account name"]').element).toHaveProperty(
      'value',
      'Personal OpenAI'
    )
    expect(dialog.text()).toContain('OpenAI')
    expect(dialog.text()).toContain('Configured')
    expect(dialog.text()).not.toContain('sk-secret')
  })

  it('renames an owned account with a name-only request', async () => {
    const wrapper = await mountView()

    await wrapper.get('[data-test="account-row-10"]').trigger('click')
    await flushPromises()
    await wrapper.get('input[aria-label="Account name"]').setValue('  Renamed account  ')
    await findButton(wrapper, 'Save name').trigger('click')
    await flushPromises()

    expect(renameAccount).toHaveBeenCalledWith(10, { name: 'Renamed account' })
    expect(showSuccess).toHaveBeenCalledWith('Name saved')
    expect(wrapper.text()).toContain('Renamed account')
  })

  it('deletes an owned account only after confirmation and reloads the list', async () => {
    const wrapper = await mountView()

    await wrapper.get('[data-test="account-row-10"]').trigger('click')
    await flushPromises()
    await findButton(wrapper, 'Delete').trigger('click')

    expect(deleteAccount).not.toHaveBeenCalled()
    expect(wrapper.get('[data-test="confirm-dialog"]').text()).toContain('Personal OpenAI')

    await wrapper.get('[data-test="confirm-delete"]').trigger('click')
    await flushPromises()

    expect(deleteAccount).toHaveBeenCalledWith(10)
    expect(showSuccess).toHaveBeenCalledWith('Account deleted')
    expect(listAccounts).toHaveBeenCalledTimes(2)
  })
})
