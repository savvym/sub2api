<template>
  <AppLayout>
    <TablePageLayout>
      <template #filters>
        <div class="space-y-3">
          <div class="flex flex-col justify-between gap-3 sm:flex-row sm:items-center">
            <form class="flex w-full max-w-xl items-center gap-2" @submit.prevent="applySearch">
              <div class="relative min-w-0 flex-1">
                <Icon
                  name="search"
                  size="sm"
                  class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-gray-400"
                />
                <input
                  v-model="searchDraft"
                  type="search"
                  class="input w-full pl-9"
                  :placeholder="t('selfServiceAccounts.searchPlaceholder')"
                  autocomplete="off"
                />
              </div>
              <button type="submit" class="btn btn-secondary btn-icon" :title="t('common.search')">
                <Icon name="search" size="md" />
              </button>
            </form>

            <div class="flex flex-shrink-0 items-center justify-end gap-2">
              <button
                type="button"
                class="btn btn-secondary btn-icon"
                :disabled="loading"
                :title="t('selfServiceAccounts.refresh')"
                @click="refreshAll"
              >
                <Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" />
              </button>
              <button
                type="button"
                class="btn btn-primary"
                :disabled="!canCreate"
                @click="openCreateDialog"
              >
                <Icon name="plus" size="md" />
                <span>{{ t('selfServiceAccounts.createAccount') }}</span>
              </button>
            </div>
          </div>

          <div
            v-if="catalogState !== 'ready' || products.length === 0"
            class="flex flex-col gap-3 rounded-lg border px-4 py-3 sm:flex-row sm:items-center sm:justify-between"
            :class="catalogNoticeClass"
            data-test="catalog-notice"
          >
            <div class="flex min-w-0 items-start gap-3">
              <Icon :name="catalogNoticeIcon" size="md" class="mt-0.5 flex-shrink-0" />
              <div class="min-w-0">
                <p class="text-sm font-semibold">{{ catalogNoticeTitle }}</p>
                <p class="mt-0.5 text-sm opacity-80">{{ catalogNoticeDescription }}</p>
              </div>
            </div>
            <button
              v-if="catalogState === 'error'"
              type="button"
              class="btn btn-secondary btn-sm flex-shrink-0"
              @click="loadProducts"
            >
              <Icon name="refresh" size="sm" />
              <span>{{ t('selfServiceAccounts.catalog.retry') }}</span>
            </button>
          </div>
        </div>
      </template>

      <template #table>
        <div
          v-if="accountsError && !loading"
          class="flex h-full min-h-64 flex-col items-center justify-center px-6 py-12 text-center"
          data-test="accounts-error"
        >
          <Icon name="exclamationTriangle" size="xl" class="text-red-500" />
          <h3 class="mt-4 text-base font-semibold text-gray-900 dark:text-white">
            {{ t('selfServiceAccounts.errors.loadAccounts') }}
          </h3>
          <p class="mt-1 max-w-lg text-sm text-gray-500 dark:text-dark-400">{{ accountsError }}</p>
          <button type="button" class="btn btn-secondary mt-5" @click="loadAccounts">
            <Icon name="refresh" size="sm" />
            <span>{{ t('common.retry') }}</span>
          </button>
        </div>

        <DataTable
          v-else
          :columns="columns"
          :data="accounts"
          :loading="loading"
          row-key="id"
          server-side-sort
          clickable-rows
          default-sort-key="updated_at"
          default-sort-order="desc"
          sort-storage-key="self-service-accounts-sort"
          @sort="handleSort"
          @row-click="openAccountDetail"
        >
          <template #cell-name="{ row }">
            <button
              type="button"
              class="max-w-[55vw] truncate text-left font-medium text-gray-900 hover:text-primary-600 dark:text-white dark:hover:text-primary-400 md:max-w-64"
              :title="row.name"
              @click.stop="openAccountDetail(row)"
            >
              {{ row.name }}
            </button>
          </template>

          <template #cell-platform="{ row }">
            <span class="inline-flex items-center gap-1.5 rounded-md bg-gray-100 px-2 py-1 text-xs font-medium text-gray-700 dark:bg-dark-700 dark:text-gray-200">
              <Icon name="globe" size="xs" />
              {{ platformLabel(row.platform) }}
            </span>
          </template>

          <template #cell-type="{ row }">
            <span class="inline-flex items-center gap-1.5 text-sm text-gray-700 dark:text-gray-300">
              <Icon name="key" size="sm" class="text-gray-400" />
              {{ typeLabel(row.type) }}
            </span>
          </template>

          <template #cell-status="{ row }">
            <StatusBadge :status="row.status" :label="statusLabel(row.status)" />
          </template>

          <template #cell-credential_configured="{ row }">
            <span
              class="inline-flex items-center gap-1.5 text-sm"
              :class="row.credential_configured ? 'text-emerald-700 dark:text-emerald-300' : 'text-amber-700 dark:text-amber-300'"
            >
              <Icon :name="row.credential_configured ? 'checkCircle' : 'exclamationCircle'" size="sm" />
              {{ row.credential_configured
                ? t('selfServiceAccounts.credential.configured')
                : t('selfServiceAccounts.credential.missing') }}
            </span>
          </template>

          <template #cell-updated_at="{ row }">
            <span class="text-sm text-gray-600 dark:text-gray-300">{{ formatDate(row.updated_at) }}</span>
          </template>

          <template #cell-actions="{ row }">
            <div class="flex items-center justify-end gap-1">
              <button
                type="button"
                class="btn btn-ghost btn-icon btn-sm"
                :title="row.owned_by_me ? t('common.edit') : t('selfServiceAccounts.detail.title')"
                @click.stop="openAccountDetail(row)"
              >
                <Icon :name="row.owned_by_me ? 'edit' : 'eye'" size="sm" />
              </button>
              <button
                v-if="row.owned_by_me"
                type="button"
                class="btn btn-ghost btn-icon btn-sm text-red-600 hover:bg-red-50 hover:text-red-700 dark:text-red-400 dark:hover:bg-red-900/20"
                :title="t('common.delete')"
                @click.stop="openDeleteDialog(row)"
              >
                <Icon name="trash" size="sm" />
              </button>
            </div>
          </template>

          <template #empty>
            <EmptyState
              :title="t('selfServiceAccounts.empty.title')"
              :description="t('selfServiceAccounts.empty.description')"
              :action-text="canCreate ? t('selfServiceAccounts.createAccount') : undefined"
              @action="openCreateDialog"
            >
              <template #icon>
                <Icon name="server" size="xl" class="h-10 w-10 text-gray-400" />
              </template>
            </EmptyState>
          </template>
        </DataTable>
      </template>

      <template v-if="!accountsError" #pagination>
        <Pagination
          :total="total"
          :page="page"
          :page-size="pageSize"
          @update:page="changePage"
          @update:page-size="changePageSize"
        />
      </template>
    </TablePageLayout>

    <BaseDialog
      :show="showCreateDialog"
      :title="t('selfServiceAccounts.create.title')"
      width="normal"
      @close="closeCreateDialog"
    >
      <div class="space-y-5">
        <div class="grid grid-cols-2 gap-2" aria-label="Creation steps">
          <div
            v-for="step in 2"
            :key="step"
            class="flex min-w-0 items-center gap-2 border-b-2 pb-2 text-sm font-medium"
            :class="createStep === step ? 'border-primary-500 text-primary-700 dark:text-primary-300' : 'border-gray-200 text-gray-400 dark:border-dark-700 dark:text-dark-500'"
          >
            <span
              class="flex h-6 w-6 flex-shrink-0 items-center justify-center rounded-full text-xs"
              :class="createStep === step ? 'bg-primary-600 text-white' : 'bg-gray-100 text-gray-500 dark:bg-dark-700 dark:text-gray-300'"
            >
              {{ step }}
            </span>
            <span class="truncate">
              {{ step === 1 ? t('selfServiceAccounts.create.productStep') : t('selfServiceAccounts.create.detailsStep') }}
            </span>
          </div>
        </div>

        <template v-if="createStep === 1">
          <p class="text-sm text-gray-600 dark:text-gray-300">
            {{ t('selfServiceAccounts.create.productDescription') }}
          </p>
          <div class="space-y-2" role="radiogroup">
            <button
              v-for="product in products"
              :key="product.id"
              type="button"
              role="radio"
              :aria-checked="selectedProductID === product.id"
              class="flex w-full items-center justify-between gap-4 rounded-lg border p-4 text-left transition-colors"
              :class="selectedProductID === product.id
                ? 'border-primary-500 bg-primary-50 dark:bg-primary-900/20'
                : 'border-gray-200 hover:border-gray-300 dark:border-dark-700 dark:hover:border-dark-600'"
              :data-test="`product-${product.id}`"
              @click="selectedProductID = product.id"
            >
              <span class="flex min-w-0 items-center gap-3">
                <span class="flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-lg bg-white text-primary-600 shadow-sm dark:bg-dark-800 dark:text-primary-400">
                  <Icon name="globe" size="md" />
                </span>
                <span class="min-w-0">
                  <span class="block truncate text-sm font-semibold text-gray-900 dark:text-white">{{ product.name }}</span>
                  <span class="mt-0.5 block text-xs text-gray-500 dark:text-dark-400">
                    {{ platformLabel(product.platform) }} · {{ typeLabel(product.type) }}
                  </span>
                </span>
              </span>
              <Icon
                :name="selectedProductID === product.id ? 'checkCircle' : 'chevronRight'"
                size="md"
                :class="selectedProductID === product.id ? 'text-primary-600 dark:text-primary-400' : 'text-gray-300 dark:text-dark-600'"
              />
            </button>
          </div>
        </template>

        <template v-else>
          <div v-if="selectedProduct" class="rounded-lg border border-gray-200 bg-gray-50 px-4 py-3 dark:border-dark-700 dark:bg-dark-900">
            <p class="text-xs font-medium uppercase text-gray-500 dark:text-dark-400">
              {{ t('selfServiceAccounts.create.selectedProduct') }}
            </p>
            <p class="mt-1 text-sm font-semibold text-gray-900 dark:text-white">{{ selectedProduct.name }}</p>
            <p class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">
              {{ platformLabel(selectedProduct.platform) }} · {{ typeLabel(selectedProduct.type) }}
            </p>
          </div>

          <Input
            v-model="createName"
            :label="t('selfServiceAccounts.create.name')"
            :placeholder="t('selfServiceAccounts.create.namePlaceholder')"
            :error="createNameError"
            required
            autocomplete="off"
            @enter="submitCreate"
          />

          <Input
            v-model="createAPIKey"
            :type="showAPIKey ? 'text' : 'password'"
            :label="t('selfServiceAccounts.create.apiKey')"
            :placeholder="t('selfServiceAccounts.create.apiKeyPlaceholder')"
            :hint="t('selfServiceAccounts.create.apiKeyHint')"
            :error="createAPIKeyError"
            required
            autocomplete="new-password"
            @enter="submitCreate"
          >
            <template #suffix>
              <button
                type="button"
                class="rounded p-1 hover:bg-gray-100 dark:hover:bg-dark-700"
                :title="showAPIKey ? t('common.hide') : t('common.show')"
                @click="showAPIKey = !showAPIKey"
              >
                <Icon :name="showAPIKey ? 'eyeOff' : 'eye'" size="sm" />
              </button>
            </template>
          </Input>

          <p v-if="createError" class="rounded-lg bg-red-50 px-3 py-2 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300">
            {{ createError }}
          </p>
        </template>
      </div>

      <template #footer>
        <div class="flex w-full flex-col-reverse gap-2 sm:flex-row sm:items-center sm:justify-between sm:gap-3">
          <button type="button" class="btn btn-secondary w-full justify-center sm:w-auto" :disabled="creating" @click="closeCreateDialog">
            {{ t('common.cancel') }}
          </button>
          <div class="flex w-full items-center gap-2 sm:w-auto">
            <button
              v-if="createStep === 2"
              type="button"
              class="btn btn-secondary min-w-0 flex-none justify-center"
              :disabled="creating"
              @click="createStep = 1"
            >
              <Icon name="arrowLeft" size="sm" />
              <span>{{ t('selfServiceAccounts.create.back') }}</span>
            </button>
            <button
              v-if="createStep === 1"
              type="button"
              class="btn btn-primary min-w-0 flex-1 justify-center sm:flex-none"
              :disabled="!selectedProductID"
              @click="createStep = 2"
            >
              <span>{{ t('selfServiceAccounts.create.continue') }}</span>
              <Icon name="arrowRight" size="sm" />
            </button>
            <button
              v-else
              type="button"
              class="btn btn-primary min-w-0 flex-1 justify-center sm:flex-none"
              :disabled="creating"
              @click="submitCreate"
            >
              <Icon name="refresh" size="sm" :class="creating ? 'animate-spin' : 'hidden'" />
              <span>{{ creating ? t('selfServiceAccounts.create.submitting') : t('selfServiceAccounts.create.submit') }}</span>
            </button>
          </div>
        </div>
      </template>
    </BaseDialog>

    <BaseDialog
      :show="showDetailDialog"
      :title="t('selfServiceAccounts.detail.title')"
      width="normal"
      @close="closeDetailDialog"
    >
      <div v-if="detailLoading" class="flex min-h-48 items-center justify-center">
        <Icon name="refresh" size="lg" class="animate-spin text-primary-500" />
        <span class="ml-3 text-sm text-gray-500 dark:text-dark-400">{{ t('selfServiceAccounts.detail.loading') }}</span>
      </div>

      <div v-else-if="detailError" class="flex min-h-48 flex-col items-center justify-center text-center">
        <Icon name="exclamationTriangle" size="xl" class="text-red-500" />
        <p class="mt-3 text-sm text-gray-600 dark:text-gray-300">{{ detailError }}</p>
        <button type="button" class="btn btn-secondary mt-4" @click="reloadAccountDetail">
          <Icon name="refresh" size="sm" />
          <span>{{ t('selfServiceAccounts.detail.retry') }}</span>
        </button>
      </div>

      <div v-else-if="detailAccount" class="space-y-5">
        <div class="grid grid-cols-1 gap-x-5 gap-y-4 rounded-lg border border-gray-200 bg-gray-50 p-4 text-sm dark:border-dark-700 dark:bg-dark-900 sm:grid-cols-2">
          <DetailField :label="t('selfServiceAccounts.detail.id')" :value="String(detailAccount.id)" />
          <DetailField :label="t('selfServiceAccounts.detail.platform')" :value="platformLabel(detailAccount.platform)" />
          <DetailField :label="t('selfServiceAccounts.detail.type')" :value="typeLabel(detailAccount.type)" />
          <DetailField :label="t('selfServiceAccounts.detail.status')" :value="statusLabel(detailAccount.status)" />
          <DetailField
            :label="t('selfServiceAccounts.detail.credential')"
            :value="detailAccount.credential_configured ? t('selfServiceAccounts.credential.configured') : t('selfServiceAccounts.credential.missing')"
          />
          <DetailField :label="t('selfServiceAccounts.detail.createdAt')" :value="formatDate(detailAccount.created_at)" />
          <DetailField class="sm:col-span-2" :label="t('selfServiceAccounts.detail.updatedAt')" :value="formatDate(detailAccount.updated_at)" />
        </div>

        <Input
          v-if="detailAccount.owned_by_me"
          v-model="detailName"
          :label="t('selfServiceAccounts.detail.name')"
          :error="detailNameError"
          required
          autocomplete="off"
          @enter="saveAccountName"
        />
        <div v-else>
          <p class="text-xs font-medium uppercase text-gray-500 dark:text-dark-400">{{ t('selfServiceAccounts.detail.name') }}</p>
          <p class="mt-1 font-medium text-gray-900 dark:text-white">{{ detailAccount.name }}</p>
          <p class="mt-3 rounded-lg bg-gray-100 px-3 py-2 text-sm text-gray-600 dark:bg-dark-800 dark:text-gray-300">
            {{ t('selfServiceAccounts.detail.readOnly') }}
          </p>
        </div>

        <p v-if="detailSaveError" class="rounded-lg bg-red-50 px-3 py-2 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300">
          {{ detailSaveError }}
        </p>
      </div>

      <template #footer>
        <div class="flex w-full flex-col-reverse gap-2 sm:flex-row sm:items-center sm:justify-between sm:gap-3">
          <button type="button" class="btn btn-secondary w-full justify-center sm:w-auto" :disabled="detailSaving" @click="closeDetailDialog">
            {{ t('common.close') }}
          </button>
          <div v-if="detailAccount?.owned_by_me" class="flex w-full items-center gap-2 sm:w-auto">
            <button
              type="button"
              class="btn btn-ghost min-w-0 flex-1 justify-center text-red-600 hover:bg-red-50 dark:text-red-400 dark:hover:bg-red-900/20 sm:flex-none"
              :disabled="detailSaving"
              @click="openDeleteDialog(detailAccount)"
            >
              <Icon name="trash" size="sm" />
              <span>{{ t('common.delete') }}</span>
            </button>
            <button
              type="button"
              class="btn btn-primary min-w-0 flex-1 justify-center sm:flex-none"
              :disabled="detailSaving || detailName.trim() === detailAccount.name"
              @click="saveAccountName"
            >
              <Icon name="refresh" size="sm" :class="detailSaving ? 'animate-spin' : 'hidden'" />
              <span>{{ detailSaving ? t('selfServiceAccounts.detail.saving') : t('selfServiceAccounts.detail.save') }}</span>
            </button>
          </div>
        </div>
      </template>
    </BaseDialog>

    <ConfirmDialog
      :show="!!deleteTarget"
      :title="t('selfServiceAccounts.delete.title')"
      :message="t('selfServiceAccounts.delete.message', { name: deleteTarget?.name || '' })"
      :confirm-text="deleting ? t('selfServiceAccounts.delete.deleting') : t('selfServiceAccounts.delete.confirm')"
      danger
      @confirm="deleteAccount"
      @cancel="closeDeleteDialog"
    />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, defineComponent, h, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import DataTable from '@/components/common/DataTable.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import Input from '@/components/common/Input.vue'
import Pagination from '@/components/common/Pagination.vue'
import StatusBadge from '@/components/common/StatusBadge.vue'
import Icon from '@/components/icons/Icon.vue'
import type { Column } from '@/components/common/types'
import { selfServiceAccountsAPI } from '@/api'
import { useAppStore } from '@/stores/app'
import type { AccountPlatform, AccountType, SelfServiceAccount, SelfServiceAccountProduct } from '@/types'
import { extractApiErrorCode, extractI18nErrorMessage } from '@/utils/apiError'
import { formatDateTimeToMinute } from '@/utils/format'

const DetailField = defineComponent({
  name: 'DetailField',
  props: {
    label: { type: String, required: true },
    value: { type: String, required: true },
  },
  setup(props, { attrs }) {
    return () => h('div', attrs, [
      h('p', { class: 'text-xs font-medium uppercase text-gray-500 dark:text-dark-400' }, props.label),
      h('p', { class: 'mt-1 break-words font-medium text-gray-900 dark:text-white' }, props.value),
    ])
  },
})

const { t, te } = useI18n()
const appStore = useAppStore()

const accounts = ref<SelfServiceAccount[]>([])
const products = ref<SelfServiceAccountProduct[]>([])
const loading = ref(false)
const accountsError = ref('')
const page = ref(1)
const pageSize = ref(20)
const total = ref(0)
const searchDraft = ref('')
const search = ref('')
const sortBy = ref<'id' | 'name' | 'platform' | 'type' | 'status' | 'created_at' | 'updated_at'>('updated_at')
const sortOrder = ref<'asc' | 'desc'>('desc')
let listController: AbortController | null = null

type CatalogState = 'loading' | 'ready' | 'forbidden' | 'error'
const catalogState = ref<CatalogState>('loading')

const showCreateDialog = ref(false)
const createStep = ref(1)
const selectedProductID = ref('')
const createName = ref('')
const createAPIKey = ref('')
const showAPIKey = ref(false)
const createNameError = ref('')
const createAPIKeyError = ref('')
const createError = ref('')
const creating = ref(false)

const showDetailDialog = ref(false)
const detailAccount = ref<SelfServiceAccount | null>(null)
const detailLoading = ref(false)
const detailError = ref('')
const detailName = ref('')
const detailNameError = ref('')
const detailSaveError = ref('')
const detailSaving = ref(false)
let detailRequestVersion = 0

const deleteTarget = ref<SelfServiceAccount | null>(null)
const deleting = ref(false)

const columns = computed<Column[]>(() => [
  { key: 'name', label: t('selfServiceAccounts.columns.name'), sortable: true },
  { key: 'platform', label: t('selfServiceAccounts.columns.platform'), sortable: true },
  { key: 'type', label: t('selfServiceAccounts.columns.type'), sortable: true },
  { key: 'status', label: t('selfServiceAccounts.columns.status'), sortable: true },
  { key: 'credential_configured', label: t('selfServiceAccounts.columns.credential') },
  { key: 'updated_at', label: t('selfServiceAccounts.columns.updatedAt'), sortable: true },
  { key: 'actions', label: t('selfServiceAccounts.columns.actions'), class: 'text-right' },
])

const selectedProduct = computed(() =>
  products.value.find((product) => product.id === selectedProductID.value) ?? null,
)
const canCreate = computed(() => catalogState.value === 'ready' && products.value.length > 0)

const catalogNoticeTitle = computed(() => {
  if (catalogState.value === 'loading') return t('common.loading')
  if (catalogState.value === 'forbidden') return t('selfServiceAccounts.catalog.forbiddenTitle')
  if (catalogState.value === 'error') return t('selfServiceAccounts.catalog.errorTitle')
  return t('selfServiceAccounts.catalog.emptyTitle')
})

const catalogNoticeDescription = computed(() => {
  if (catalogState.value === 'loading') return t('selfServiceAccounts.create.productDescription')
  if (catalogState.value === 'forbidden') return t('selfServiceAccounts.catalog.forbiddenDescription')
  if (catalogState.value === 'error') return t('selfServiceAccounts.catalog.errorDescription')
  return t('selfServiceAccounts.catalog.emptyDescription')
})

const catalogNoticeIcon = computed<'refresh' | 'lock' | 'exclamationTriangle' | 'infoCircle'>(() => {
  if (catalogState.value === 'loading') return 'refresh'
  if (catalogState.value === 'forbidden') return 'lock'
  if (catalogState.value === 'error') return 'exclamationTriangle'
  return 'infoCircle'
})

const catalogNoticeClass = computed(() => {
  if (catalogState.value === 'error') {
    return 'border-red-200 bg-red-50 text-red-800 dark:border-red-900/50 dark:bg-red-900/20 dark:text-red-200'
  }
  if (catalogState.value === 'forbidden') {
    return 'border-gray-200 bg-gray-50 text-gray-700 dark:border-dark-700 dark:bg-dark-900 dark:text-gray-200'
  }
  return 'border-amber-200 bg-amber-50 text-amber-800 dark:border-amber-900/50 dark:bg-amber-900/20 dark:text-amber-200'
})

function accountError(error: unknown, fallbackKey: string): string {
  return extractI18nErrorMessage(
    error,
    t,
    'selfServiceAccounts.errors',
    t(fallbackKey),
  )
}

function isCanceled(error: unknown): boolean {
  if (!error || typeof error !== 'object') return false
  const candidate = error as { name?: string; code?: string }
  return candidate.name === 'AbortError' || candidate.name === 'CanceledError' || candidate.code === 'ERR_CANCELED'
}

async function loadAccounts(): Promise<void> {
  listController?.abort()
  const controller = new AbortController()
  listController = controller
  loading.value = true
  accountsError.value = ''
  try {
    const result = await selfServiceAccountsAPI.list({
      page: page.value,
      page_size: pageSize.value,
      search: search.value || undefined,
      sort_by: sortBy.value,
      sort_order: sortOrder.value,
    }, { signal: controller.signal })
    if (controller.signal.aborted) return
    accounts.value = result.items
    total.value = result.total
  } catch (error) {
    if (controller.signal.aborted || isCanceled(error)) return
    accounts.value = []
    total.value = 0
    accountsError.value = accountError(error, 'selfServiceAccounts.errors.loadAccounts')
  } finally {
    if (listController === controller) {
      loading.value = false
      listController = null
    }
  }
}

async function loadProducts(): Promise<void> {
  catalogState.value = 'loading'
  try {
    products.value = await selfServiceAccountsAPI.listProducts()
    catalogState.value = 'ready'
    if (!products.value.some((product) => product.id === selectedProductID.value)) {
      selectedProductID.value = products.value[0]?.id ?? ''
    }
  } catch (error) {
    products.value = []
    selectedProductID.value = ''
    catalogState.value = extractApiErrorCode(error) === 'SELF_SERVICE_ACCOUNT_FORBIDDEN'
      ? 'forbidden'
      : 'error'
  }
}

function refreshAll(): void {
  void Promise.all([loadAccounts(), loadProducts()])
}

function applySearch(): void {
  const nextSearch = searchDraft.value.trim()
  if (search.value === nextSearch && page.value === 1) {
    void loadAccounts()
    return
  }
  search.value = nextSearch
  page.value = 1
  void loadAccounts()
}

function handleSort(key: string, order: 'asc' | 'desc'): void {
  const allowed = new Set(['id', 'name', 'platform', 'type', 'status', 'created_at', 'updated_at'])
  if (!allowed.has(key)) return
  sortBy.value = key as typeof sortBy.value
  sortOrder.value = order
  page.value = 1
  void loadAccounts()
}

function changePage(nextPage: number): void {
  page.value = nextPage
  void loadAccounts()
}

function changePageSize(nextPageSize: number): void {
  pageSize.value = nextPageSize
  page.value = 1
  void loadAccounts()
}

function platformLabel(platform: AccountPlatform): string {
  if (platform === 'openai') return 'OpenAI'
  if (platform === 'anthropic') return 'Anthropic'
  if (platform === 'gemini') return 'Gemini'
  return platform.charAt(0).toUpperCase() + platform.slice(1)
}

function typeLabel(type: AccountType): string {
  if (type === 'apikey') return 'API Key'
  if (type === 'setup-token') return 'Setup Token'
  if (type === 'service_account') return 'Service Account'
  return type.charAt(0).toUpperCase() + type.slice(1)
}

function statusLabel(status: string): string {
  const key = `selfServiceAccounts.status.${status}`
  return te(key) ? t(key) : t('selfServiceAccounts.status.unknown')
}

function formatDate(value: string): string {
  return formatDateTimeToMinute(value) || '-'
}

function resetCreateForm(): void {
  createStep.value = 1
  selectedProductID.value = products.value[0]?.id ?? ''
  createName.value = ''
  createAPIKey.value = ''
  showAPIKey.value = false
  createNameError.value = ''
  createAPIKeyError.value = ''
  createError.value = ''
}

function openCreateDialog(): void {
  if (!canCreate.value) return
  resetCreateForm()
  showCreateDialog.value = true
}

function closeCreateDialog(): void {
  if (creating.value) return
  showCreateDialog.value = false
  resetCreateForm()
}

function validateCreateForm(): boolean {
  createNameError.value = ''
  createAPIKeyError.value = ''
  const name = createName.value.trim()
  const apiKey = createAPIKey.value.trim()
  if (!name) {
    createNameError.value = t('selfServiceAccounts.errors.invalidName')
  }
  if (!apiKey || /\s/.test(apiKey)) {
    createAPIKeyError.value = t('selfServiceAccounts.errors.invalidApiKey')
  }
  return !createNameError.value && !createAPIKeyError.value && !!selectedProduct.value
}

async function submitCreate(): Promise<void> {
  if (createStep.value !== 2 || creating.value || !validateCreateForm() || !selectedProduct.value) return
  creating.value = true
  createError.value = ''
  try {
    await selfServiceAccountsAPI.create({
      name: createName.value.trim(),
      product_id: selectedProduct.value.id,
      api_key: createAPIKey.value.trim(),
    })
    createAPIKey.value = ''
    showCreateDialog.value = false
    resetCreateForm()
    appStore.showSuccess(t('selfServiceAccounts.create.success'))
    page.value = 1
    await loadAccounts()
  } catch (error) {
    createAPIKey.value = ''
    createError.value = accountError(error, 'selfServiceAccounts.errors.create')
    if (extractApiErrorCode(error) === 'SELF_SERVICE_ACCOUNT_PRODUCT_UNAVAILABLE') {
      await loadProducts()
      createStep.value = 1
    }
  } finally {
    creating.value = false
  }
}

function replaceAccount(updated: SelfServiceAccount): void {
  const index = accounts.value.findIndex((account) => account.id === updated.id)
  if (index >= 0) accounts.value.splice(index, 1, updated)
}

function openAccountDetail(account: SelfServiceAccount): void {
  showDetailDialog.value = true
  detailAccount.value = account
  detailName.value = account.name
  detailNameError.value = ''
  detailSaveError.value = ''
  void reloadAccountDetail()
}

async function reloadAccountDetail(): Promise<void> {
  const accountID = detailAccount.value?.id
  if (!accountID) return
  const requestVersion = ++detailRequestVersion
  detailLoading.value = true
  detailError.value = ''
  try {
    const account = await selfServiceAccountsAPI.getById(accountID)
    if (requestVersion !== detailRequestVersion || !showDetailDialog.value) return
    detailAccount.value = account
    detailName.value = account.name
    replaceAccount(account)
  } catch (error) {
    if (requestVersion !== detailRequestVersion || !showDetailDialog.value) return
    detailError.value = accountError(error, 'selfServiceAccounts.errors.loadDetail')
  } finally {
    if (requestVersion === detailRequestVersion) detailLoading.value = false
  }
}

function closeDetailDialog(): void {
  if (detailSaving.value) return
  detailRequestVersion++
  showDetailDialog.value = false
  detailAccount.value = null
  detailError.value = ''
  detailSaveError.value = ''
  detailNameError.value = ''
}

async function saveAccountName(): Promise<void> {
  if (!detailAccount.value?.owned_by_me || detailSaving.value) return
  const name = detailName.value.trim()
  detailNameError.value = name ? '' : t('selfServiceAccounts.errors.invalidName')
  if (detailNameError.value || name === detailAccount.value.name) return
  detailSaving.value = true
  detailSaveError.value = ''
  try {
    const updated = await selfServiceAccountsAPI.rename(detailAccount.value.id, { name })
    detailAccount.value = updated
    detailName.value = updated.name
    replaceAccount(updated)
    appStore.showSuccess(t('selfServiceAccounts.detail.saveSuccess'))
  } catch (error) {
    detailSaveError.value = accountError(error, 'selfServiceAccounts.errors.rename')
  } finally {
    detailSaving.value = false
  }
}

function openDeleteDialog(account: SelfServiceAccount): void {
  if (!account.owned_by_me || deleting.value) return
  deleteTarget.value = account
}

function closeDeleteDialog(): void {
  if (deleting.value) return
  deleteTarget.value = null
}

async function deleteAccount(): Promise<void> {
  const target = deleteTarget.value
  if (!target || deleting.value) return
  deleting.value = true
  try {
    await selfServiceAccountsAPI.delete(target.id)
    deleteTarget.value = null
    if (detailAccount.value?.id === target.id) closeDetailDialog()
    appStore.showSuccess(t('selfServiceAccounts.delete.success'))
    if (accounts.value.length === 1 && page.value > 1) page.value--
    await loadAccounts()
  } catch (error) {
    appStore.showError(accountError(error, 'selfServiceAccounts.errors.delete'))
  } finally {
    deleting.value = false
  }
}

onMounted(() => {
  refreshAll()
})

onBeforeUnmount(() => {
  listController?.abort()
  detailRequestVersion++
  createAPIKey.value = ''
})
</script>
