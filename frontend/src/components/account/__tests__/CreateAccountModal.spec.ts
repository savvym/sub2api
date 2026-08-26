import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const {
  createAccountMock,
  createAccountInGroupMock,
  generateAuthUrlMock,
  exchangeCodeMock,
  probeUpstreamBillingMock,
  syncUpstreamModelsMock,
  showWarningMock,
  importCodexSessionMock,
  createOpenAICodexPATMock,
  checkMixedChannelRiskMock,
  authIsSimpleMode,
} = vi.hoisted(() => ({
  createAccountMock: vi.fn(),
  createAccountInGroupMock: vi.fn(),
  generateAuthUrlMock: vi.fn(),
  exchangeCodeMock: vi.fn(),
  probeUpstreamBillingMock: vi.fn(),
  syncUpstreamModelsMock: vi.fn(),
  showWarningMock: vi.fn(),
  importCodexSessionMock: vi.fn(),
  createOpenAICodexPATMock: vi.fn(),
  checkMixedChannelRiskMock: vi.fn(),
  authIsSimpleMode: { value: true },
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: vi.fn(),
    showSuccess: vi.fn(),
    showWarning: showWarningMock,
  }),
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({
    get isSimpleMode() {
      return authIsSimpleMode.value
    },
  }),
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      create: createAccountMock,
      createInGroup: createAccountInGroupMock,
      generateAuthUrl: generateAuthUrlMock,
      exchangeCode: exchangeCodeMock,
      probeUpstreamBilling: probeUpstreamBillingMock,
      syncUpstreamModels: syncUpstreamModelsMock,
      checkMixedChannelRisk: checkMixedChannelRiskMock,
      importCodexSession: importCodexSessionMock,
      createOpenAICodexPAT: createOpenAICodexPATMock,
    },
    settings: {
      getWebSearchEmulationConfig: vi.fn().mockResolvedValue({ enabled: false, providers: [] }),
      getSettings: vi.fn().mockResolvedValue({}),
    },
    tlsFingerprintProfiles: {
      list: vi.fn().mockResolvedValue([]),
    },
  },
}))

vi.mock('@/api/admin/accounts', () => ({
  getAntigravityDefaultModelMapping: vi.fn().mockResolvedValue([]),
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key }),
  }
})

import CreateAccountModal from '../CreateAccountModal.vue'

const BaseDialogStub = defineComponent({
  name: 'BaseDialog',
  props: { show: { type: Boolean, default: false } },
  template: '<div v-if="show"><slot /><slot name="footer" /></div>',
})

const OAuthAuthorizationFlowStub = defineComponent({
  name: 'OAuthAuthorizationFlow',
  props: {
    allowMultiple: Boolean,
    showCookieOption: Boolean,
    showRefreshTokenOption: Boolean,
    showMobileRefreshTokenOption: Boolean,
    showManualOption: Boolean,
    showCodexSessionImportOption: Boolean,
    showAgentIdentityOption: Boolean,
    showCodexPatOption: Boolean,
    showSsoOption: Boolean,
    initialInputMethod: String,
  },
  data: () => ({
    inputMethod: 'manual',
    authCode: '',
    oauthState: '',
    projectId: '',
  }),
  emits: ['generate-url', 'import-codex-session', 'import-codex-pat'],
  template: `
    <div>
      <button data-testid="generate-oauth-url" @click="$emit('generate-url')">generate</button>
      <button data-testid="import-codex-session" @click="$emit('import-codex-session', 'session-json')">session</button>
      <button data-testid="import-codex-pat" @click="$emit('import-codex-pat', 'pat-token')">pat</button>
    </div>
  `,
})

const ConfirmDialogStub = defineComponent({
  name: 'ConfirmDialog',
  props: {
    show: {
      type: Boolean,
      default: false,
    },
    message: String,
  },
  emits: ['confirm', 'cancel'],
  template: `
    <button
      v-if="show"
      type="button"
      data-testid="confirm-mixed-channel"
      @click="$emit('confirm')"
    >
      confirm
    </button>
  `,
})

const GroupSelectorStub = defineComponent({
  name: 'GroupSelector',
  props: {
    modelValue: {
      type: Array,
      default: () => [],
    },
    lockedGroupIds: {
      type: Array,
      default: () => [],
    },
  },
  emits: ['update:modelValue'],
  template: `
    <button
      type="button"
      data-testid="select-pricing-groups"
      @click="$emit('update:modelValue', [1, 2])"
    >
      groups
    </button>
  `,
})

const ModelWhitelistSelectorStub = defineComponent({
  name: 'ModelWhitelistSelector',
  props: {
    modelValue: {
      type: Array,
      default: () => [],
    },
    platform: String,
    syncCredentials: Object,
  },
  emits: ['update:modelValue', 'upstream-synced'],
  template: `<button
    type="button"
    data-testid="model-whitelist-selector"
    @click="$emit('update:modelValue', ['public-glm']); $emit('upstream-synced')"
  >models</button>`,
})

interface CreateContextProps {
  initialPlatform?: 'anthropic' | 'openai' | 'gemini' | 'antigravity' | 'grok' | 'kimi' | 'zhipu' | 'deepseek'
  lockPlatform?: boolean
  requiredGroupId?: number
}

function mountModal(groups: any[] = [], contextProps: CreateContextProps = {}) {
  return mount(CreateAccountModal, {
    props: { show: true, proxies: [], groups, ...contextProps },
    global: {
      stubs: {
        BaseDialog: BaseDialogStub,
        OAuthAuthorizationFlow: OAuthAuthorizationFlowStub,
        ConfirmDialog: ConfirmDialogStub,
        Select: true,
        Icon: true,
        PlatformIcon: true,
        ProxySelector: true,
        ProxyAdBanner: true,
        GroupSelector: GroupSelectorStub,
        ModelWhitelistSelector: ModelWhitelistSelectorStub,
        QuotaLimitCard: true,
      },
    },
  })
}

async function selectButtonByText(wrapper: ReturnType<typeof mountModal>, text: string) {
  const button = wrapper.findAll('button').find((candidate) => candidate.text().includes(text))
  expect(button).toBeDefined()
  await button?.trigger('click')
}

async function submitApiKeyAccount(
  platform: 'openai' | 'anthropic',
  enableLongContextBilling = false,
  disableUpstreamBillingProbe = false
) {
  const wrapper = mountModal()
  await selectButtonByText(wrapper, platform === 'openai' ? 'OpenAI' : 'admin.accounts.claudeConsole')
  if (platform === 'openai') {
    await selectButtonByText(wrapper, 'API Key')
  }
  await wrapper.get('form#create-account-form input[type="text"]').setValue(`${platform} account`)
  await wrapper.get('form#create-account-form input[type="password"]').setValue('test-api-key')
  if (enableLongContextBilling) {
    await wrapper.get('[data-testid="openai-long-context-billing-toggle"]').trigger('click')
  }
  if (disableUpstreamBillingProbe) {
    await wrapper.get('[data-testid="upstream-billing-auto-probe"]').trigger('click')
  }
  await wrapper.get('form#create-account-form').trigger('submit.prevent')
  await flushPromises()
  return wrapper
}

async function openCodexImportStep(toggleClicks = 0) {
  const wrapper = mountModal()
  await selectButtonByText(wrapper, 'OpenAI')
  for (let click = 0; click < toggleClicks; click += 1) {
    await wrapper.get('[data-testid="openai-long-context-billing-toggle"]').trigger('click')
  }
  await wrapper.get('form#create-account-form input[type="text"]').setValue('Codex import')
  await wrapper.get('form#create-account-form').trigger('submit.prevent')
  return wrapper
}

async function submitOpenAIGroupOAuth(groupId: number) {
  const wrapper = mountModal(
    [{ id: groupId, platform: 'openai', long_context_pricing_enabled: false }],
    { initialPlatform: 'openai', lockPlatform: true, requiredGroupId: groupId }
  )
  await wrapper.get('form#create-account-form input[type="text"]').setValue('OpenAI OAuth account')
  await wrapper.get('form#create-account-form').trigger('submit.prevent')

  const flow = wrapper.getComponent(OAuthAuthorizationFlowStub)
  await flow.get('[data-testid="generate-oauth-url"]').trigger('click')
  await flushPromises()
  ;(flow.vm as any).authCode = 'oauth-code'
  ;(flow.vm as any).oauthState = 'oauth-state'
  await wrapper.vm.$nextTick()
  await wrapper.get('[data-testid="complete-oauth"]').trigger('click')
  await flushPromises()
  return wrapper
}

describe('CreateAccountModal OpenAI long-context billing', () => {
  beforeEach(() => {
    authIsSimpleMode.value = true
    createAccountMock.mockReset().mockResolvedValue({ id: 42, platform: 'openai', type: 'apikey' })
    createAccountInGroupMock.mockReset().mockResolvedValue({ id: 42, platform: 'openai', type: 'apikey' })
    generateAuthUrlMock.mockReset().mockResolvedValue({
      auth_url: 'https://auth.example/authorize?state=oauth-state',
      session_id: 'oauth-session',
    })
    exchangeCodeMock.mockReset().mockResolvedValue({
      access_token: 'oauth-access-token',
      refresh_token: 'oauth-refresh-token',
      expires_at: 1_800_000_000,
    })
    probeUpstreamBillingMock.mockReset().mockResolvedValue({})
    syncUpstreamModelsMock.mockReset().mockResolvedValue({ models: [], metadata: {} })
    showWarningMock.mockReset()
    importCodexSessionMock.mockReset().mockResolvedValue({
      created: 1,
      updated: 0,
      skipped: 0,
      failed: 0,
      errors: [],
      warnings: [],
    })
    createOpenAICodexPATMock.mockReset().mockResolvedValue({})
    checkMixedChannelRiskMock.mockReset().mockResolvedValue({ has_risk: false })
  })

  it('applies and locks a non-default initial platform for a normal group', async () => {
    authIsSimpleMode.value = false
    const wrapper = mountModal(
      [{ id: 6, platform: 'openai', long_context_pricing_enabled: false }],
      { initialPlatform: 'openai', lockPlatform: true, requiredGroupId: 6 }
    )
    await flushPromises()

    const anthropicPlatformButton = wrapper
      .findAll('button')
      .find((button) => button.text().trim() === 'Anthropic')
    expect(anthropicPlatformButton?.attributes('disabled')).toBeDefined()

    await selectButtonByText(wrapper, 'API Key')
    await wrapper.get('form#create-account-form input[type="text"]').setValue('Locked OpenAI')
    await wrapper.get('form#create-account-form input[type="password"]').setValue('sk-openai-test')
    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(checkMixedChannelRiskMock).not.toHaveBeenCalled()
    expect(createAccountMock).not.toHaveBeenCalled()
    expect(createAccountInGroupMock).toHaveBeenCalledTimes(1)
    expect(createAccountInGroupMock.mock.calls[0]?.[0]).toBe(6)
    expect(createAccountInGroupMock.mock.calls[0]?.[1]).toMatchObject({
      platform: 'openai',
      group_ids: [6],
    })
  })

  it('locks a normal group platform and preserves its required group through reset and risk confirmation', async () => {
    authIsSimpleMode.value = false
    createAccountInGroupMock
      .mockRejectedValueOnce({
        status: 409,
        reason: 'mixed_channel_warning',
        message: 'Concurrent risk',
        metadata: { risk_confirmation_token: 'group-create-risk-token' },
      })
      .mockResolvedValueOnce({ id: 42, platform: 'anthropic', type: 'apikey' })
    const wrapper = mountModal(
      [
        { id: 7, platform: 'anthropic', long_context_pricing_enabled: false },
        { id: 8, platform: 'anthropic', long_context_pricing_enabled: false },
      ],
      { initialPlatform: 'anthropic', lockPlatform: true, requiredGroupId: 7 }
    )

    const openAIPlatformButton = wrapper
      .findAll('button')
      .find((button) => button.text().trim() === 'OpenAI')
    expect(openAIPlatformButton?.attributes('disabled')).toBeDefined()
    expect(wrapper.getComponent(GroupSelectorStub).props('modelValue')).toEqual([7])
    expect(wrapper.getComponent(GroupSelectorStub).props('lockedGroupIds')).toEqual([7])

    await wrapper.get('[data-testid="select-pricing-groups"]').trigger('click')
    expect(wrapper.getComponent(GroupSelectorStub).props('modelValue')).toEqual([7, 1, 2])

    await wrapper.setProps({ show: false })
    await wrapper.setProps({ requiredGroupId: 8 })
    await wrapper.setProps({ show: true })
    expect(wrapper.getComponent(GroupSelectorStub).props('modelValue')).toEqual([8])

    await wrapper.get('[data-testid="select-pricing-groups"]').trigger('click')
    expect(wrapper.getComponent(GroupSelectorStub).props('modelValue')).toEqual([8, 1, 2])

    await selectButtonByText(wrapper, 'admin.accounts.claudeConsole')
    await wrapper.get('form#create-account-form input[type="text"]').setValue('Anthropic account')
    await wrapper.get('form#create-account-form input[type="password"]').setValue('sk-ant-test')
    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(checkMixedChannelRiskMock).not.toHaveBeenCalled()
    expect(createAccountMock).not.toHaveBeenCalled()
    expect(createAccountInGroupMock).toHaveBeenCalledTimes(1)
    expect(createAccountInGroupMock.mock.calls[0]?.[0]).toBe(8)
    expect(createAccountInGroupMock.mock.calls[0]?.[1]).toMatchObject({
      platform: 'anthropic',
      group_ids: [8, 1, 2],
    })
    expect(createAccountInGroupMock.mock.calls[0]?.[1]?.confirm_mixed_channel_risk).toBeUndefined()
    expect(createAccountInGroupMock.mock.calls[0]?.[1]?.risk_confirmation_token).toBeUndefined()

    await wrapper.get('[data-testid="confirm-mixed-channel"]').trigger('click')
    await flushPromises()

    expect(createAccountMock).not.toHaveBeenCalled()
    expect(createAccountInGroupMock).toHaveBeenCalledTimes(2)
    expect(createAccountInGroupMock.mock.calls[1]?.[0]).toBe(8)
    expect(createAccountInGroupMock.mock.calls[1]?.[1]).toMatchObject({
      platform: 'anthropic',
      group_ids: [8, 1, 2],
      risk_confirmation_token: 'group-create-risk-token',
    })
    expect(createAccountInGroupMock.mock.calls[1]?.[1]?.confirm_mixed_channel_risk).toBeUndefined()
  })

  it('does not retry a group create when mixed-channel confirmation is cancelled', async () => {
    createAccountInGroupMock.mockRejectedValueOnce({
      status: 409,
      error: 'mixed_channel_warning',
      message: 'Concurrent risk',
      metadata: { risk_confirmation_token: 'unused-risk-token' },
    })
    const wrapper = mountModal(
      [{ id: 12, platform: 'anthropic', long_context_pricing_enabled: false }],
      { initialPlatform: 'anthropic', lockPlatform: true, requiredGroupId: 12 }
    )

    await selectButtonByText(wrapper, 'admin.accounts.claudeConsole')
    await wrapper.get('form#create-account-form input[type="text"]').setValue('Cancelled account')
    await wrapper.get('form#create-account-form input[type="password"]').setValue('sk-ant-test')
    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(createAccountInGroupMock).toHaveBeenCalledTimes(1)
    expect(wrapper.getComponent(ConfirmDialogStub).props('show')).toBe(true)

    wrapper.getComponent(ConfirmDialogStub).vm.$emit('cancel')
    await flushPromises()

    expect(createAccountInGroupMock).toHaveBeenCalledTimes(1)
    expect(wrapper.getComponent(ConfirmDialogStub).props('show')).toBe(false)
  })

  it.each([
    ['an unknown network result', { status: 0 }],
    ['a server failure', { status: 503 }],
  ])('retries the cached OAuth create payload after %s without exchanging again', async (_label, error) => {
    const createdAccount = { id: 73, platform: 'openai', type: 'oauth' }
    createAccountInGroupMock
      .mockRejectedValueOnce(error)
      .mockResolvedValueOnce(createdAccount)

    const wrapper = await submitOpenAIGroupOAuth(16)

    expect(exchangeCodeMock).toHaveBeenCalledTimes(1)
    expect(createAccountInGroupMock).toHaveBeenCalledTimes(1)
    expect(wrapper.find('[data-testid="complete-oauth"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="retry-group-create"]').exists()).toBe(true)
    const originalPayload = createAccountInGroupMock.mock.calls[0]?.[1]

    await wrapper.get('[data-testid="retry-group-create"]').trigger('click')
    await flushPromises()

    expect(exchangeCodeMock).toHaveBeenCalledTimes(1)
    expect(createAccountInGroupMock).toHaveBeenCalledTimes(2)
    expect(createAccountInGroupMock.mock.calls[1]?.[1]).toEqual(originalPayload)
    expect(wrapper.emitted('created')?.[0]?.[0]).toEqual(createdAccount)
  })

  it('retries a cached token-confirmed OAuth create without exchanging again', async () => {
    const createdAccount = { id: 74, platform: 'openai', type: 'oauth' }
    createAccountInGroupMock
      .mockRejectedValueOnce({
        status: 409,
        reason: 'mixed_channel_warning',
        metadata: { risk_confirmation_token: 'oauth-risk-token' },
      })
      .mockRejectedValueOnce({ status: 0 })
      .mockResolvedValueOnce(createdAccount)

    const wrapper = await submitOpenAIGroupOAuth(17)
    await wrapper.get('[data-testid="confirm-mixed-channel"]').trigger('click')
    await flushPromises()

    expect(exchangeCodeMock).toHaveBeenCalledTimes(1)
    expect(createAccountInGroupMock).toHaveBeenCalledTimes(2)
    const confirmedPayload = createAccountInGroupMock.mock.calls[1]?.[1]
    expect(confirmedPayload?.risk_confirmation_token).toBe('oauth-risk-token')
    expect(wrapper.find('[data-testid="retry-group-create"]').exists()).toBe(true)

    await wrapper.get('[data-testid="retry-group-create"]').trigger('click')
    await flushPromises()

    expect(exchangeCodeMock).toHaveBeenCalledTimes(1)
    expect(createAccountInGroupMock).toHaveBeenCalledTimes(3)
    expect(createAccountInGroupMock.mock.calls[2]?.[1]).toEqual(confirmedPayload)
    expect(wrapper.emitted('created')?.[0]?.[0]).toEqual(createdAccount)
  })

  it.each([
    ['an unknown network result', { status: 0 }],
    ['a server failure', { status: 503 }],
    ['an in-progress idempotent request', { status: 409, reason: 'IDEMPOTENCY_IN_PROGRESS' }],
    ['an idempotency retry backoff', { status: 409, reason: 'IDEMPOTENCY_RETRY_BACKOFF' }],
  ])('reuses the confirmed group-create payload after %s', async (_label, retryableError) => {
    createAccountInGroupMock
      .mockRejectedValueOnce({
        status: 409,
        reason: 'mixed_channel_warning',
        message: 'Concurrent risk',
        metadata: { risk_confirmation_token: 'retry-risk-token' },
      })
      .mockRejectedValueOnce(retryableError)
      .mockResolvedValueOnce({ id: 42, platform: 'anthropic', type: 'apikey' })
    const wrapper = mountModal(
      [{ id: 13, platform: 'anthropic', long_context_pricing_enabled: false }],
      { initialPlatform: 'anthropic', lockPlatform: true, requiredGroupId: 13 }
    )

    await selectButtonByText(wrapper, 'admin.accounts.claudeConsole')
    await wrapper.get('form#create-account-form input[type="text"]').setValue('Retry account')
    await wrapper.get('form#create-account-form input[type="password"]').setValue('sk-ant-test')
    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await flushPromises()
    await wrapper.get('[data-testid="confirm-mixed-channel"]').trigger('click')
    await flushPromises()

    expect(createAccountInGroupMock).toHaveBeenCalledTimes(2)
    const confirmedPayload = createAccountInGroupMock.mock.calls[1]?.[1]
    expect(confirmedPayload?.risk_confirmation_token).toBe('retry-risk-token')

    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(createAccountInGroupMock).toHaveBeenCalledTimes(3)
    expect(createAccountInGroupMock.mock.calls[2]?.[0]).toBe(13)
    expect(createAccountInGroupMock.mock.calls[2]?.[1]).toEqual(confirmedPayload)
  })

  it('drops a confirmed group-create retry after a definitive failure', async () => {
    createAccountInGroupMock
      .mockRejectedValueOnce({
        status: 409,
        reason: 'mixed_channel_warning',
        metadata: { risk_confirmation_token: 'stale-risk-token' },
      })
      .mockRejectedValueOnce({ status: 422, reason: 'account_group_policy_violation' })
      .mockResolvedValueOnce({ id: 42, platform: 'anthropic', type: 'apikey' })
    const wrapper = mountModal(
      [{ id: 14, platform: 'anthropic', long_context_pricing_enabled: false }],
      { initialPlatform: 'anthropic', lockPlatform: true, requiredGroupId: 14 }
    )

    await selectButtonByText(wrapper, 'admin.accounts.claudeConsole')
    await wrapper.get('form#create-account-form input[type="text"]').setValue('Definitive failure')
    await wrapper.get('form#create-account-form input[type="password"]').setValue('sk-ant-test')
    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await flushPromises()
    await wrapper.get('[data-testid="confirm-mixed-channel"]').trigger('click')
    await flushPromises()
    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(createAccountInGroupMock).toHaveBeenCalledTimes(3)
    expect(createAccountInGroupMock.mock.calls[1]?.[1]?.risk_confirmation_token).toBe('stale-risk-token')
    expect(createAccountInGroupMock.mock.calls[2]?.[1]?.risk_confirmation_token).toBeUndefined()
  })

  it('drops a confirmed group-create retry when the payload changes', async () => {
    createAccountInGroupMock
      .mockRejectedValueOnce({
        status: 409,
        reason: 'mixed_channel_warning',
        metadata: { risk_confirmation_token: 'old-payload-risk-token' },
      })
      .mockRejectedValueOnce({ status: 0 })
      .mockResolvedValueOnce({ id: 42, platform: 'anthropic', type: 'apikey' })
    const wrapper = mountModal(
      [{ id: 15, platform: 'anthropic', long_context_pricing_enabled: false }],
      { initialPlatform: 'anthropic', lockPlatform: true, requiredGroupId: 15 }
    )

    await selectButtonByText(wrapper, 'admin.accounts.claudeConsole')
    const nameInput = wrapper.get('form#create-account-form input[type="text"]')
    await nameInput.setValue('Original payload')
    await wrapper.get('form#create-account-form input[type="password"]').setValue('sk-ant-test')
    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await flushPromises()
    await wrapper.get('[data-testid="confirm-mixed-channel"]').trigger('click')
    await flushPromises()

    await nameInput.setValue('Changed payload')
    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(createAccountInGroupMock).toHaveBeenCalledTimes(3)
    expect(createAccountInGroupMock.mock.calls[2]?.[1]).toMatchObject({ name: 'Changed payload' })
    expect(createAccountInGroupMock.mock.calls[2]?.[1]?.risk_confirmation_token).toBeUndefined()
  })

  it('keeps a Composite group required while leaving platform selection unlocked', async () => {
    authIsSimpleMode.value = false
    const wrapper = mountModal(
      [{ id: 9, platform: 'composite', long_context_pricing_enabled: false }],
      { requiredGroupId: 9 }
    )

    const openAIPlatformButton = wrapper
      .findAll('button')
      .find((button) => button.text().trim() === 'OpenAI')
    const anthropicPlatformButton = wrapper
      .findAll('button')
      .find((button) => button.text().trim() === 'Anthropic')
    expect(openAIPlatformButton?.attributes('disabled')).toBeUndefined()
    expect(anthropicPlatformButton?.classes()).not.toContain('bg-white')
    expect(wrapper.get('[data-tour="account-form-submit"]').attributes('disabled')).toBeDefined()
    expect(wrapper.find('[data-testid="select-pricing-groups"]').exists()).toBe(false)

    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await flushPromises()
    expect(createAccountInGroupMock).not.toHaveBeenCalled()

    await openAIPlatformButton?.trigger('click')
    expect(wrapper.get('[data-tour="account-form-submit"]').attributes('disabled')).toBeUndefined()
    expect(wrapper.find('[data-testid="select-pricing-groups"]').exists()).toBe(true)
    await selectButtonByText(wrapper, 'API Key')
    await wrapper.get('[data-testid="select-pricing-groups"]').trigger('click')

    await wrapper.get('form#create-account-form input[type="text"]').setValue('Composite OpenAI')
    await wrapper.get('form#create-account-form input[type="password"]').setValue('sk-openai-test')
    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(createAccountMock).not.toHaveBeenCalled()
    expect(createAccountInGroupMock).toHaveBeenCalledTimes(1)
    expect(createAccountInGroupMock.mock.calls[0]?.[0]).toBe(9)
    expect(createAccountInGroupMock.mock.calls[0]?.[1]).toMatchObject({
      platform: 'openai',
      group_ids: [9, 1, 2],
    })
  })

  it.each(['anthropic', 'openai', 'antigravity', 'grok'] as const)(
    'limits %s group creation to single-account authorization methods',
    async (platform) => {
      const wrapper = mountModal(
        [{ id: 10, platform, long_context_pricing_enabled: false }],
        { initialPlatform: platform, lockPlatform: true, requiredGroupId: 10 }
      )
      await wrapper
        .get('form#create-account-form input[type="text"]')
        .setValue(`${platform} group account`)
      await wrapper.get('form#create-account-form').trigger('submit.prevent')

      const flow = wrapper.getComponent(OAuthAuthorizationFlowStub)
      expect(flow.props()).toMatchObject({
        allowMultiple: false,
        showCookieOption: false,
        showRefreshTokenOption: false,
        showMobileRefreshTokenOption: false,
        showCodexSessionImportOption: false,
        showAgentIdentityOption: false,
        showCodexPatOption: false,
        showSsoOption: false,
        showManualOption: true,
      })
    }
  )

  it('ignores disabled OpenAI import events in group creation context', async () => {
    const wrapper = mountModal(
      [{ id: 11, platform: 'openai', long_context_pricing_enabled: false }],
      { initialPlatform: 'openai', lockPlatform: true, requiredGroupId: 11 }
    )
    await wrapper
      .get('form#create-account-form input[type="text"]')
      .setValue('OpenAI group account')
    await wrapper.get('form#create-account-form').trigger('submit.prevent')

    const flow = wrapper.getComponent(OAuthAuthorizationFlowStub)
    flow.vm.$emit('import-codex-session', 'session-json')
    flow.vm.$emit('import-codex-pat', 'pat-token')
    await flushPromises()

    expect(importCodexSessionMock).not.toHaveBeenCalled()
    expect(createOpenAICodexPATMock).not.toHaveBeenCalled()
    expect(createAccountMock).not.toHaveBeenCalled()
  })

  it('retries a create request after a flat mixed-channel warning is confirmed', async () => {
    createAccountMock
      .mockRejectedValueOnce({
        status: 409,
        error: 'mixed_channel_warning',
        message: 'Concurrent risk',
      })
      .mockResolvedValueOnce({ id: 42, platform: 'anthropic', type: 'apikey' })

    const wrapper = await submitApiKeyAccount('anthropic')

    expect(createAccountMock).toHaveBeenCalledTimes(1)
    expect(createAccountMock.mock.calls[0]?.[0]?.confirm_mixed_channel_risk).toBeUndefined()
    expect(wrapper.getComponent(ConfirmDialogStub).props('show')).toBe(true)
    expect(wrapper.getComponent(ConfirmDialogStub).props('message')).toBe('Concurrent risk')

    await wrapper.get('[data-testid="confirm-mixed-channel"]').trigger('click')
    await flushPromises()

    expect(createAccountMock).toHaveBeenCalledTimes(2)
    expect(createAccountMock.mock.calls[1]?.[0]?.confirm_mixed_channel_risk).toBe(true)
  })

  it('hides only the redundant account toggle when every selected group enables tier pricing', async () => {
    authIsSimpleMode.value = false
    const wrapper = mountModal([
      { id: 1, long_context_pricing_enabled: true },
      { id: 2, long_context_pricing_enabled: true },
    ])

    await selectButtonByText(wrapper, 'OpenAI')
    await wrapper.get('[data-testid="select-pricing-groups"]').trigger('click')

    expect(wrapper.find('[data-testid="openai-long-context-billing-toggle"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="create-openai-ws-mode"]').exists()).toBe(true)
  })

  it('keeps the account toggle when any selected group disables tier pricing', async () => {
    authIsSimpleMode.value = false
    const wrapper = mountModal([
      { id: 1, long_context_pricing_enabled: true },
      { id: 2, long_context_pricing_enabled: false },
    ])

    await selectButtonByText(wrapper, 'OpenAI')
    await wrapper.get('[data-testid="select-pricing-groups"]').trigger('click')

    expect(wrapper.find('[data-testid="openai-long-context-billing-toggle"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="create-openai-ws-mode"]').exists()).toBe(true)
  })

  it('sends false explicitly for normal OpenAI account creation by default', async () => {
    await submitApiKeyAccount('openai')

    expect(createAccountMock).toHaveBeenCalledTimes(1)
    expect(createAccountMock.mock.calls[0]?.[0]?.extra?.openai_long_context_billing_enabled).toBe(false)
  })

  it('persists upstream model metadata after creating an account from preview', async () => {
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'OpenAI')
    await selectButtonByText(wrapper, 'API Key')
    await wrapper.get('form#create-account-form input[type="text"]').setValue('OpenCode account')
    await wrapper.get('form#create-account-form input[type="password"]').setValue('test-api-key')
    await wrapper.get('[data-testid="model-whitelist-selector"]').trigger('click')
    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(createAccountMock).toHaveBeenCalledOnce()
    expect(syncUpstreamModelsMock).toHaveBeenCalledWith(42)
  })

  it('persists upstream model metadata after creating an account in a group', async () => {
    authIsSimpleMode.value = false
    const wrapper = mountModal(
      [{ id: 16, platform: 'openai', long_context_pricing_enabled: false }],
      { initialPlatform: 'openai', lockPlatform: true, requiredGroupId: 16 }
    )
    await flushPromises()

    await selectButtonByText(wrapper, 'API Key')
    await wrapper.get('form#create-account-form input[type="text"]').setValue('Grouped account')
    await wrapper.get('form#create-account-form input[type="password"]').setValue('test-api-key')
    await wrapper.get('[data-testid="model-whitelist-selector"]').trigger('click')
    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(createAccountInGroupMock).toHaveBeenCalledWith(
      16,
      expect.objectContaining({ platform: 'openai', type: 'apikey' })
    )
    expect(createAccountMock).not.toHaveBeenCalled()
    expect(syncUpstreamModelsMock).toHaveBeenCalledWith(42)
  })

  it('includes the current concrete model mapping in preview credentials', async () => {
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'OpenAI')
    await selectButtonByText(wrapper, 'API Key')
    await wrapper.get('form#create-account-form input[type="password"]').setValue('test-api-key')
    await wrapper.get('[data-testid="model-whitelist-selector"]').trigger('click')
    await flushPromises()

    expect(wrapper.getComponent(ModelWhitelistSelectorStub).props('syncCredentials')).toMatchObject({
      model_mapping: { 'public-glm': 'public-glm' }
    })
  })

  it('runs formal capability sync after creating an account with explicit mappings', async () => {
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'OpenAI')
    await selectButtonByText(wrapper, 'API Key')
    await wrapper.get('form#create-account-form input[type="text"]').setValue('Mapped account')
    await wrapper.get('form#create-account-form input[type="password"]').setValue('test-api-key')
    await selectButtonByText(wrapper, 'admin.accounts.modelMapping')
    await selectButtonByText(wrapper, 'admin.accounts.addMapping')
    await wrapper.get('input[placeholder="admin.accounts.requestModel"]').setValue('public-glm')
    await wrapper.get('input[placeholder="admin.accounts.actualModel"]').setValue('glm-5.3')
    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(createAccountMock.mock.calls[0]?.[0]?.credentials?.model_mapping).toEqual({
      'public-glm': 'glm-5.3'
    })
    expect(syncUpstreamModelsMock).toHaveBeenCalledWith(42)
  })

  it('warns when post-create capability metadata remains incomplete', async () => {
    syncUpstreamModelsMock.mockResolvedValue({
      models: ['x-preview-f-free'],
      warnings: [{ code: 'upstream_model_metadata_incomplete', message: 'metadata incomplete' }],
    })
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'OpenAI')
    await selectButtonByText(wrapper, 'API Key')
    await wrapper.get('form#create-account-form input[type="text"]').setValue('OpenCode account')
    await wrapper.get('form#create-account-form input[type="password"]').setValue('test-api-key')
    await wrapper.get('[data-testid="model-whitelist-selector"]').trigger('click')
    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(showWarningMock).toHaveBeenCalledWith(
      'admin.accounts.syncUpstreamModelsMetadataIncomplete'
    )
  })

  // namespace 摊平是仅 OAuth 的兼容开关：API Key 走 chat completions 回退桥时由桥自行摊平
  it('shows the Codex namespace flatten toggle only for OpenAI OAuth accounts', async () => {
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'OpenAI')

    expect(wrapper.find('[data-testid="create-openai-flatten-namespaces-toggle"]').exists()).toBe(
      true
    )

    await selectButtonByText(wrapper, 'API Key')
    expect(wrapper.find('[data-testid="create-openai-flatten-namespaces-toggle"]').exists()).toBe(
      false
    )
  })

  it('enables upstream billing probes by default for new OpenAI API key accounts', async () => {
    await submitApiKeyAccount('openai')

    expect(createAccountMock.mock.calls[0]?.[0]?.upstream_billing_probe_enabled).toBe(true)
  })

  it('waits for the initial upstream billing probe before refreshing the account list', async () => {
    let resolveProbe: (() => void) | undefined
    probeUpstreamBillingMock.mockImplementationOnce(
      () => new Promise<void>((resolve) => {
        resolveProbe = resolve
      })
    )

    const wrapper = await submitApiKeyAccount('openai')

    expect(probeUpstreamBillingMock).toHaveBeenCalledWith(42)
    expect(wrapper.emitted('created')).toBeUndefined()

    resolveProbe?.()
    await flushPromises()

    expect(wrapper.emitted('created')).toHaveLength(1)
  })

  it('sends an explicit disabled state when the create toggle is turned off', async () => {
    await submitApiKeyAccount('openai', false, true)

    expect(createAccountMock.mock.calls[0]?.[0]?.upstream_billing_probe_enabled).toBe(false)
    expect(probeUpstreamBillingMock).not.toHaveBeenCalled()
  })

  it('submits adaptive Kimi protocol endpoints', async () => {
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'Kimi')
    await wrapper.get('form#create-account-form input[type="text"]').setValue('Kimi adaptive')
    await wrapper.get('form#create-account-form input[type="password"]').setValue('sk-kimi')

    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(createAccountMock).toHaveBeenCalledTimes(1)
    expect(createAccountMock.mock.calls[0]?.[0]?.credentials).toMatchObject({
      account_mode: 'payg',
      api_protocol: 'adaptive',
      base_url: 'https://api.moonshot.cn/v1',
      api_base_urls: {
        chat_completions: 'https://api.moonshot.cn/v1',
        anthropic: 'https://api.moonshot.cn/anthropic'
      }
    })
  })

  it('uses the edited adaptive Chat endpoint when previewing upstream models', async () => {
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'Kimi')
    await wrapper
      .get('[data-testid="cn-adaptive-base-url-chat_completions"]')
      .setValue('https://relay.example.com/v1')
    await wrapper.get('form#create-account-form input[type="password"]').setValue('sk-relay')

    expect(wrapper.getComponent(ModelWhitelistSelectorStub).props('syncCredentials')).toMatchObject({
      platform: 'kimi',
      type: 'apikey',
      base_url: 'https://relay.example.com/v1',
      api_key: 'sk-relay'
    })
  })

  it('exposes Agent Identity in the OpenAI authorization methods', async () => {
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'OpenAI')
    await wrapper.get('form#create-account-form input[type="text"]').setValue('OpenAI account')
    await wrapper.get('form#create-account-form').trigger('submit.prevent')

    const flow = wrapper.getComponent(OAuthAuthorizationFlowStub)
    expect(flow.props('showRefreshTokenOption')).toBe(true)
    expect(flow.props('showMobileRefreshTokenOption')).toBe(true)
    expect(flow.props('showManualOption')).toBe(true)
    expect(flow.props('showCodexSessionImportOption')).toBe(true)
    expect(flow.props('showAgentIdentityOption')).toBe(true)
    expect(flow.props('showCodexPatOption')).toBe(true)
    expect(flow.props('initialInputMethod')).toBe('manual')
  })

  it.each([
    ['anthropic', { allowMultiple: true, showCookieOption: true }],
    ['antigravity', { showRefreshTokenOption: true }],
    ['grok', { showRefreshTokenOption: true, showSsoOption: true }],
  ] as const)('keeps regular %s account authorization methods available', async (platform, expectedProps) => {
    const wrapper = mountModal()
    if (platform !== 'anthropic') {
      await selectButtonByText(wrapper, platform === 'antigravity' ? 'Antigravity' : 'Grok')
    }
    await wrapper
      .get('form#create-account-form input[type="text"]')
      .setValue(`${platform} account`)
    await wrapper.get('form#create-account-form').trigger('submit.prevent')

    const flow = wrapper.getComponent(OAuthAuthorizationFlowStub)
    expect(flow.props()).toMatchObject({ showManualOption: true, ...expectedProps })
  })

  it.each([
    ['camelCase', { authMode: 'agentIdentity', agentIdentity: { agentRuntimeId: 'runtime' } }],
    ['nested identity without auth_mode', { agent_identity: { agent_runtime_id: 'runtime' } }],
  ])('accepts backend-compatible %s Agent Identity imports', async (_name, content) => {
    const wrapper = await openCodexImportStep()
    const flow = wrapper.getComponent(OAuthAuthorizationFlowStub)
    flow.vm.inputMethod = 'agent_identity'

    flow.vm.$emit('import-codex-session', JSON.stringify(content))
    await flushPromises()

    expect(importCodexSessionMock).toHaveBeenCalledTimes(1)
  })

  it('sends true explicitly when OpenAI long-context billing is enabled', async () => {
    await submitApiKeyAccount('openai', true)

    expect(createAccountMock).toHaveBeenCalledTimes(1)
    expect(createAccountMock.mock.calls[0]?.[0]?.extra?.openai_long_context_billing_enabled).toBe(true)
  })

  it('omits the OpenAI setting for non-OpenAI account creation', async () => {
    await submitApiKeyAccount('anthropic')

    expect(createAccountMock).toHaveBeenCalledTimes(1)
    expect(createAccountMock.mock.calls[0]?.[0]?.extra?.openai_long_context_billing_enabled).toBeUndefined()
    // 上游倍率探测已放宽到全部 API-key 平台：非 OpenAI 平台与 OpenAI 一致，默认开启。
    expect(createAccountMock.mock.calls[0]?.[0]?.upstream_billing_probe_enabled).toBe(true)
  })

  it('sends an explicit disabled state when the non-OpenAI create toggle is turned off', async () => {
    await submitApiKeyAccount('anthropic', false, true)

    expect(createAccountMock.mock.calls[0]?.[0]?.upstream_billing_probe_enabled).toBe(false)
  })

  it('antigravity upstream 创建默认携带上游倍率探测开关', async () => {
    // antigravity upstream 走独立创建 helper，
    // 也必须与其余 API-key 平台一样默认开启探测并传递开关。
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'Antigravity')
    await selectButtonByText(wrapper, 'admin.accounts.types.antigravityApikey')
    await wrapper.get('form#create-account-form input[type="text"]').setValue('antigravity relay')
    const baseInput = wrapper
      .findAll('input')
      .find((candidate) => candidate.attributes('placeholder') === 'https://cloudcode-pa.googleapis.com')
    expect(baseInput).toBeDefined()
    await baseInput?.setValue('https://relay.example')
    await wrapper.get('form#create-account-form input[type="password"]').setValue('sk-upstream')
    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(createAccountMock).toHaveBeenCalledTimes(1)
    const payload = createAccountMock.mock.calls[0]?.[0]
    expect(payload?.platform).toBe('antigravity')
    expect(payload?.type).toBe('apikey')
    expect(payload?.upstream_billing_probe_enabled).toBe(true)
    // 创建成功后前端立即发起一次首探（与其他 apikey 平台一致）。
    expect(probeUpstreamBillingMock).toHaveBeenCalledWith(42)
  })

  it('leaves Codex session import billing ownership to the backend', async () => {
    const wrapper = await openCodexImportStep()
    await wrapper.get('[data-testid="import-codex-session"]').trigger('click')
    await flushPromises()

    expect(importCodexSessionMock).toHaveBeenCalledTimes(1)
    expect(importCodexSessionMock.mock.calls[0]?.[0]?.extra?.openai_long_context_billing_enabled).toBeUndefined()
  })

  it('leaves Codex PAT import billing ownership to the backend', async () => {
    const wrapper = await openCodexImportStep()
    await wrapper.get('[data-testid="import-codex-pat"]').trigger('click')
    await flushPromises()

    expect(createOpenAICodexPATMock).toHaveBeenCalledTimes(1)
    expect(createOpenAICodexPATMock.mock.calls[0]?.[0]?.extra?.openai_long_context_billing_enabled).toBeUndefined()
  })

  it('sends explicit true for Codex session import after the toggle is enabled', async () => {
    const wrapper = await openCodexImportStep(1)
    await wrapper.get('[data-testid="import-codex-session"]').trigger('click')
    await flushPromises()

    expect(importCodexSessionMock.mock.calls[0]?.[0]?.extra?.openai_long_context_billing_enabled).toBe(true)
  })

  it('sends explicit false for Codex session import after the toggle is changed back', async () => {
    const wrapper = await openCodexImportStep(2)
    await wrapper.get('[data-testid="import-codex-session"]').trigger('click')
    await flushPromises()

    expect(importCodexSessionMock.mock.calls[0]?.[0]?.extra?.openai_long_context_billing_enabled).toBe(false)
  })

  it('sends explicit true for Codex PAT import after the toggle is enabled', async () => {
    const wrapper = await openCodexImportStep(1)
    await wrapper.get('[data-testid="import-codex-pat"]').trigger('click')
    await flushPromises()

    expect(createOpenAICodexPATMock.mock.calls[0]?.[0]?.extra?.openai_long_context_billing_enabled).toBe(true)
  })

  it('sends explicit false for Codex PAT import after the toggle is changed back', async () => {
    const wrapper = await openCodexImportStep(2)
    await wrapper.get('[data-testid="import-codex-pat"]').trigger('click')
    await flushPromises()

    expect(createOpenAICodexPATMock.mock.calls[0]?.[0]?.extra?.openai_long_context_billing_enabled).toBe(false)
  })
})
