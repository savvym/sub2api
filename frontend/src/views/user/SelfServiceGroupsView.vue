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
                  :placeholder="t('selfServiceGroups.searchPlaceholder')"
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
                :title="t('selfServiceGroups.refresh')"
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
                <span>{{ t('selfServiceGroups.createGroup') }}</span>
              </button>
            </div>
          </div>

          <div
            v-if="catalogState !== 'ready' || platforms.length === 0"
            class="flex flex-col gap-3 rounded-lg border px-4 py-3 sm:flex-row sm:items-center sm:justify-between"
            :class="catalogNoticeClass"
            data-test="group-catalog-notice"
          >
            <div class="flex min-w-0 items-start gap-3">
              <Icon
                :name="catalogNoticeIcon"
                size="md"
                class="mt-0.5 flex-shrink-0"
                :class="catalogState === 'loading' ? 'animate-spin' : ''"
              />
              <div class="min-w-0">
                <p class="text-sm font-semibold">{{ catalogNoticeTitle }}</p>
                <p class="mt-0.5 text-sm opacity-80">{{ catalogNoticeDescription }}</p>
              </div>
            </div>
            <button
              v-if="catalogState === 'error'"
              type="button"
              class="btn btn-secondary btn-sm flex-shrink-0"
              @click="loadPlatforms"
            >
              <Icon name="refresh" size="sm" />
              <span>{{ t('selfServiceGroups.catalog.retry') }}</span>
            </button>
          </div>
        </div>
      </template>

      <template #table>
        <div
          v-if="groupsError && !loading"
          class="flex h-full min-h-64 flex-col items-center justify-center px-6 py-12 text-center"
          data-test="groups-error"
        >
          <Icon name="exclamationTriangle" size="xl" class="text-red-500" />
          <h3 class="mt-4 text-base font-semibold text-gray-900 dark:text-white">
            {{ t('selfServiceGroups.errors.loadGroups') }}
          </h3>
          <p class="mt-1 max-w-lg text-sm text-gray-500 dark:text-dark-400">{{ groupsError }}</p>
          <button type="button" class="btn btn-secondary mt-5" @click="loadGroups">
            <Icon name="refresh" size="sm" />
            <span>{{ t('common.retry') }}</span>
          </button>
        </div>

        <DataTable
          v-else
          :columns="columns"
          :data="groups"
          :loading="loading"
          row-key="id"
          server-side-sort
          clickable-rows
          default-sort-key="updated_at"
          default-sort-order="desc"
          sort-storage-key="self-service-groups-sort"
          @sort="handleSort"
          @row-click="openGroupDetail"
        >
          <template #cell-name="{ row }">
            <button
              type="button"
              class="max-w-[55vw] truncate text-left font-medium text-gray-900 hover:text-primary-600 dark:text-white dark:hover:text-primary-400 md:max-w-64"
              :title="row.name"
              @click.stop="openGroupDetail(row)"
            >
              {{ row.name }}
            </button>
          </template>

          <template #cell-description="{ row }">
            <span
              class="block max-w-[65vw] truncate text-sm text-gray-600 dark:text-gray-300 md:max-w-80"
              :class="row.description ? '' : 'italic text-gray-400 dark:text-dark-500'"
              :title="row.description || t('selfServiceGroups.detail.noDescription')"
            >
              {{ row.description || t('selfServiceGroups.detail.noDescription') }}
            </span>
          </template>

          <template #cell-platform="{ row }">
            <span class="inline-flex items-center gap-1.5 rounded-md bg-gray-100 px-2 py-1 text-xs font-medium text-gray-700 dark:bg-dark-700 dark:text-gray-200">
              <Icon name="grid" size="xs" />
              {{ platformLabel(row.platform) }}
            </span>
          </template>

          <template #cell-status="{ row }">
            <StatusBadge :status="row.status" :label="statusLabel(row.status)" />
          </template>

          <template #cell-updated_at="{ row }">
            <span class="text-sm text-gray-600 dark:text-gray-300">{{ formatDate(row.updated_at) }}</span>
          </template>

          <template #cell-actions="{ row }">
            <div class="flex items-center justify-end gap-1">
              <button
                type="button"
                class="btn btn-ghost btn-icon btn-sm"
                :title="row.owned_by_me ? t('common.edit') : t('selfServiceGroups.detail.title')"
                @click.stop="openGroupDetail(row)"
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
              :title="t('selfServiceGroups.empty.title')"
              :description="t('selfServiceGroups.empty.description')"
              :action-text="canCreate ? t('selfServiceGroups.createGroup') : undefined"
              @action="openCreateDialog"
            >
              <template #icon>
                <Icon name="grid" size="xl" class="h-10 w-10 text-gray-400" />
              </template>
            </EmptyState>
          </template>
        </DataTable>
      </template>

      <template v-if="!groupsError" #pagination>
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
      :title="t('selfServiceGroups.create.title')"
      width="normal"
      @close="closeCreateDialog"
    >
      <div class="space-y-5">
        <div>
          <p class="input-label mb-1.5 block">{{ t('selfServiceGroups.create.platform') }}</p>
          <p class="mb-3 text-sm text-gray-500 dark:text-dark-400">
            {{ t('selfServiceGroups.create.platformDescription') }}
          </p>
          <div class="space-y-2" role="radiogroup" :aria-label="t('selfServiceGroups.create.platform')">
            <button
              v-for="platform in platforms"
              :key="platform.id"
              type="button"
              role="radio"
              :aria-checked="selectedPlatformID === platform.id"
              class="flex w-full items-center justify-between gap-4 rounded-lg border p-4 text-left transition-colors"
              :class="selectedPlatformID === platform.id
                ? 'border-primary-500 bg-primary-50 dark:bg-primary-900/20'
                : 'border-gray-200 hover:border-gray-300 dark:border-dark-700 dark:hover:border-dark-600'"
              :data-test="`group-platform-${platform.id}`"
              @click="selectedPlatformID = platform.id"
            >
              <span class="flex min-w-0 items-center gap-3">
                <span class="flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-lg bg-white text-primary-600 shadow-sm dark:bg-dark-800 dark:text-primary-400">
                  <Icon name="grid" size="md" />
                </span>
                <span class="min-w-0">
                  <span class="block truncate text-sm font-semibold text-gray-900 dark:text-white">{{ platform.name }}</span>
                  <span class="mt-0.5 block text-xs text-gray-500 dark:text-dark-400">{{ platformLabel(platform.platform) }}</span>
                </span>
              </span>
              <Icon
                :name="selectedPlatformID === platform.id ? 'checkCircle' : 'chevronRight'"
                size="md"
                :class="selectedPlatformID === platform.id ? 'text-primary-600 dark:text-primary-400' : 'text-gray-300 dark:text-dark-600'"
              />
            </button>
          </div>
        </div>

        <Input
          v-model="createName"
          :label="t('selfServiceGroups.create.name')"
          :placeholder="t('selfServiceGroups.create.namePlaceholder')"
          :error="createNameError"
          required
          autocomplete="off"
          @enter="submitCreate"
        />

        <div>
          <div class="mb-1.5 flex items-center justify-between gap-3">
            <label for="self-service-group-create-description" class="input-label">
              {{ t('selfServiceGroups.create.description') }}
            </label>
            <span class="text-xs text-gray-400 dark:text-dark-500">
              {{ t('selfServiceGroups.create.descriptionCount', { count: runeLength(createDescription) }) }}
            </span>
          </div>
          <textarea
            id="self-service-group-create-description"
            v-model="createDescription"
            rows="4"
            class="input min-h-24 w-full resize-y"
            :class="createDescriptionError ? 'input-error ring-2 ring-red-500/20' : ''"
            :placeholder="t('selfServiceGroups.create.descriptionPlaceholder')"
          ></textarea>
          <p v-if="createDescriptionError" class="input-error-text mt-1.5">{{ createDescriptionError }}</p>
        </div>

        <p v-if="createError" class="rounded-lg bg-red-50 px-3 py-2 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300">
          {{ createError }}
        </p>
      </div>

      <template #footer>
        <div class="flex w-full flex-col-reverse gap-2 sm:flex-row sm:items-center sm:justify-end sm:gap-3">
          <button type="button" class="btn btn-secondary w-full justify-center sm:w-auto" :disabled="creating" @click="closeCreateDialog">
            {{ t('common.cancel') }}
          </button>
          <button
            type="button"
            class="btn btn-primary w-full justify-center sm:w-auto"
            :disabled="creating || !selectedPlatformID"
            @click="submitCreate"
          >
            <Icon name="refresh" size="sm" :class="creating ? 'animate-spin' : 'hidden'" />
            <span>{{ creating ? t('selfServiceGroups.create.submitting') : t('selfServiceGroups.create.submit') }}</span>
          </button>
        </div>
      </template>
    </BaseDialog>

    <BaseDialog
      :show="showDetailDialog"
      :title="t('selfServiceGroups.detail.title')"
      width="normal"
      @close="closeDetailDialog"
    >
      <div v-if="detailLoading" class="flex min-h-48 items-center justify-center">
        <Icon name="refresh" size="lg" class="animate-spin text-primary-500" />
        <span class="ml-3 text-sm text-gray-500 dark:text-dark-400">{{ t('selfServiceGroups.detail.loading') }}</span>
      </div>

      <div v-else-if="detailError" class="flex min-h-48 flex-col items-center justify-center text-center">
        <Icon name="exclamationTriangle" size="xl" class="text-red-500" />
        <p class="mt-3 text-sm text-gray-600 dark:text-gray-300">{{ detailError }}</p>
        <button type="button" class="btn btn-secondary mt-4" @click="reloadGroupDetail">
          <Icon name="refresh" size="sm" />
          <span>{{ t('selfServiceGroups.detail.retry') }}</span>
        </button>
      </div>

      <div v-else-if="detailGroup" class="space-y-5">
        <div class="grid grid-cols-1 gap-x-5 gap-y-4 rounded-lg border border-gray-200 bg-gray-50 p-4 text-sm dark:border-dark-700 dark:bg-dark-900 sm:grid-cols-2">
          <DetailField :label="t('selfServiceGroups.detail.id')" :value="String(detailGroup.id)" />
          <DetailField :label="t('selfServiceGroups.detail.platform')" :value="platformLabel(detailGroup.platform)" />
          <DetailField :label="t('selfServiceGroups.detail.status')" :value="statusLabel(detailGroup.status)" />
          <DetailField :label="t('selfServiceGroups.detail.createdAt')" :value="formatDate(detailGroup.created_at)" />
          <DetailField class="sm:col-span-2" :label="t('selfServiceGroups.detail.updatedAt')" :value="formatDate(detailGroup.updated_at)" />
        </div>

        <template v-if="detailGroup.owned_by_me">
          <Input
            v-model="detailName"
            :label="t('selfServiceGroups.detail.name')"
            :error="detailNameError"
            required
            autocomplete="off"
            @enter="saveGroup"
          />

          <div>
            <div class="mb-1.5 flex items-center justify-between gap-3">
              <label for="self-service-group-detail-description" class="input-label">
                {{ t('selfServiceGroups.detail.description') }}
              </label>
              <span class="text-xs text-gray-400 dark:text-dark-500">
                {{ t('selfServiceGroups.create.descriptionCount', { count: runeLength(detailDescription) }) }}
              </span>
            </div>
            <textarea
              id="self-service-group-detail-description"
              v-model="detailDescription"
              rows="5"
              class="input min-h-28 w-full resize-y"
              :class="detailDescriptionError ? 'input-error ring-2 ring-red-500/20' : ''"
              :placeholder="t('selfServiceGroups.detail.descriptionPlaceholder')"
            ></textarea>
            <p v-if="detailDescriptionError" class="input-error-text mt-1.5">{{ detailDescriptionError }}</p>
          </div>
        </template>

        <div v-else class="space-y-4">
          <div>
            <p class="text-xs font-medium uppercase text-gray-500 dark:text-dark-400">{{ t('selfServiceGroups.detail.name') }}</p>
            <p class="mt-1 font-medium text-gray-900 dark:text-white">{{ detailGroup.name }}</p>
          </div>
          <div>
            <p class="text-xs font-medium uppercase text-gray-500 dark:text-dark-400">{{ t('selfServiceGroups.detail.description') }}</p>
            <p class="mt-1 whitespace-pre-wrap text-sm text-gray-700 dark:text-gray-300">
              {{ detailGroup.description || t('selfServiceGroups.detail.noDescription') }}
            </p>
          </div>
          <p class="rounded-lg bg-gray-100 px-3 py-2 text-sm text-gray-600 dark:bg-dark-800 dark:text-gray-300">
            {{ t('selfServiceGroups.detail.readOnly') }}
          </p>
        </div>

        <div class="flex items-start gap-2 rounded-lg border border-gray-200 bg-white px-3 py-2.5 text-sm text-gray-600 dark:border-dark-700 dark:bg-dark-800 dark:text-gray-300">
          <Icon name="lock" size="sm" class="mt-0.5 flex-shrink-0 text-gray-400" />
          <span>{{ t('selfServiceGroups.detail.managedFields') }}</span>
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
          <div v-if="detailGroup?.owned_by_me" class="flex w-full items-center gap-2 sm:w-auto">
            <button
              type="button"
              class="btn btn-ghost min-w-0 flex-1 justify-center text-red-600 hover:bg-red-50 dark:text-red-400 dark:hover:bg-red-900/20 sm:flex-none"
              :disabled="detailSaving"
              @click="openDeleteDialog(detailGroup)"
            >
              <Icon name="trash" size="sm" />
              <span>{{ t('common.delete') }}</span>
            </button>
            <button
              type="button"
              class="btn btn-primary min-w-0 flex-1 justify-center sm:flex-none"
              :disabled="detailSaving || !hasDetailChanges"
              @click="saveGroup"
            >
              <Icon name="refresh" size="sm" :class="detailSaving ? 'animate-spin' : 'hidden'" />
              <span>{{ detailSaving ? t('selfServiceGroups.detail.saving') : t('selfServiceGroups.detail.save') }}</span>
            </button>
          </div>
        </div>
      </template>
    </BaseDialog>

    <ConfirmDialog
      :show="!!deleteTarget"
      :title="t('selfServiceGroups.delete.title')"
      :message="t('selfServiceGroups.delete.message', { name: deleteTarget?.name || '' })"
      :confirm-text="deleting ? t('selfServiceGroups.delete.deleting') : t('selfServiceGroups.delete.confirm')"
      danger
      @confirm="deleteGroup"
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
import { selfServiceGroupsAPI } from '@/api'
import { useAppStore } from '@/stores/app'
import type {
  AccountPlatform,
  SelfServiceGroup,
  SelfServiceGroupPlatform,
  UpdateSelfServiceGroupRequest,
} from '@/types'
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

const groups = ref<SelfServiceGroup[]>([])
const platforms = ref<SelfServiceGroupPlatform[]>([])
const loading = ref(false)
const groupsError = ref('')
const page = ref(1)
const pageSize = ref(20)
const total = ref(0)
const searchDraft = ref('')
const search = ref('')
const sortBy = ref<'id' | 'name' | 'platform' | 'status' | 'created_at' | 'updated_at'>('updated_at')
const sortOrder = ref<'asc' | 'desc'>('desc')
let listController: AbortController | null = null

type CatalogState = 'loading' | 'ready' | 'forbidden' | 'error'
const catalogState = ref<CatalogState>('loading')

const showCreateDialog = ref(false)
const selectedPlatformID = ref('')
const createName = ref('')
const createDescription = ref('')
const createNameError = ref('')
const createDescriptionError = ref('')
const createError = ref('')
const creating = ref(false)

const showDetailDialog = ref(false)
const detailGroup = ref<SelfServiceGroup | null>(null)
const detailLoading = ref(false)
const detailError = ref('')
const detailName = ref('')
const detailDescription = ref('')
const detailNameError = ref('')
const detailDescriptionError = ref('')
const detailSaveError = ref('')
const detailSaving = ref(false)
let detailRequestVersion = 0

const deleteTarget = ref<SelfServiceGroup | null>(null)
const deleting = ref(false)

const columns = computed<Column[]>(() => [
  { key: 'name', label: t('selfServiceGroups.columns.name'), sortable: true },
  { key: 'description', label: t('selfServiceGroups.columns.description') },
  { key: 'platform', label: t('selfServiceGroups.columns.platform'), sortable: true },
  { key: 'status', label: t('selfServiceGroups.columns.status'), sortable: true },
  { key: 'updated_at', label: t('selfServiceGroups.columns.updatedAt'), sortable: true },
  { key: 'actions', label: t('selfServiceGroups.columns.actions'), class: 'text-right' },
])

const canCreate = computed(() => catalogState.value === 'ready' && platforms.value.length > 0)
const hasDetailChanges = computed(() => {
  if (!detailGroup.value?.owned_by_me) return false
  return normalizeName(detailName.value) !== detailGroup.value.name ||
    normalizeDescription(detailDescription.value) !== detailGroup.value.description
})

const catalogNoticeTitle = computed(() => {
  if (catalogState.value === 'loading') return t('common.loading')
  if (catalogState.value === 'forbidden') return t('selfServiceGroups.catalog.forbiddenTitle')
  if (catalogState.value === 'error') return t('selfServiceGroups.catalog.errorTitle')
  return t('selfServiceGroups.catalog.emptyTitle')
})

const catalogNoticeDescription = computed(() => {
  if (catalogState.value === 'loading') return t('selfServiceGroups.create.platformDescription')
  if (catalogState.value === 'forbidden') return t('selfServiceGroups.catalog.forbiddenDescription')
  if (catalogState.value === 'error') return t('selfServiceGroups.catalog.errorDescription')
  return t('selfServiceGroups.catalog.emptyDescription')
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

function groupError(error: unknown, fallbackKey: string): string {
  return extractI18nErrorMessage(
    error,
    t,
    'selfServiceGroups.errors',
    t(fallbackKey),
  )
}

function isCanceled(error: unknown): boolean {
  if (!error || typeof error !== 'object') return false
  const candidate = error as { name?: string; code?: string }
  return candidate.name === 'AbortError' || candidate.name === 'CanceledError' || candidate.code === 'ERR_CANCELED'
}

async function loadGroups(): Promise<void> {
  listController?.abort()
  const controller = new AbortController()
  listController = controller
  loading.value = true
  groupsError.value = ''
  try {
    const result = await selfServiceGroupsAPI.list({
      page: page.value,
      page_size: pageSize.value,
      search: search.value || undefined,
      sort_by: sortBy.value,
      sort_order: sortOrder.value,
    }, { signal: controller.signal })
    if (controller.signal.aborted) return
    groups.value = result.items
    total.value = result.total
  } catch (error) {
    if (controller.signal.aborted || isCanceled(error)) return
    groups.value = []
    total.value = 0
    groupsError.value = groupError(error, 'selfServiceGroups.errors.loadGroups')
  } finally {
    if (listController === controller) {
      loading.value = false
      listController = null
    }
  }
}

async function loadPlatforms(): Promise<void> {
  catalogState.value = 'loading'
  try {
    platforms.value = await selfServiceGroupsAPI.listPlatforms()
    catalogState.value = 'ready'
    if (!platforms.value.some((platform) => platform.id === selectedPlatformID.value)) {
      selectedPlatformID.value = platforms.value[0]?.id ?? ''
    }
  } catch (error) {
    platforms.value = []
    selectedPlatformID.value = ''
    catalogState.value = extractApiErrorCode(error) === 'SELF_SERVICE_GROUP_FORBIDDEN'
      ? 'forbidden'
      : 'error'
  }
}

function refreshAll(): void {
  void Promise.all([loadGroups(), loadPlatforms()])
}

function applySearch(): void {
  const nextSearch = searchDraft.value.trim()
  if (search.value === nextSearch && page.value === 1) {
    void loadGroups()
    return
  }
  search.value = nextSearch
  page.value = 1
  void loadGroups()
}

function handleSort(key: string, order: 'asc' | 'desc'): void {
  const allowed = new Set(['id', 'name', 'platform', 'status', 'created_at', 'updated_at'])
  if (!allowed.has(key)) return
  sortBy.value = key as typeof sortBy.value
  sortOrder.value = order
  page.value = 1
  void loadGroups()
}

function changePage(nextPage: number): void {
  page.value = nextPage
  void loadGroups()
}

function changePageSize(nextPageSize: number): void {
  pageSize.value = nextPageSize
  page.value = 1
  void loadGroups()
}

function platformLabel(platform: AccountPlatform): string {
  if (platform === 'openai') return 'OpenAI'
  if (platform === 'anthropic') return 'Anthropic'
  if (platform === 'gemini') return 'Gemini'
  if (platform === 'antigravity') return 'Antigravity'
  if (platform === 'grok') return 'Grok'
  return platform.charAt(0).toUpperCase() + platform.slice(1)
}

function statusLabel(status: string): string {
  const key = `selfServiceGroups.status.${status}`
  return te(key) ? t(key) : t('selfServiceGroups.status.unknown')
}

function formatDate(value: string): string {
  return formatDateTimeToMinute(value) || '-'
}

function runeLength(value: string): number {
  return Array.from(value).length
}

function normalizeName(value: string): string {
  return value.trim()
}

function normalizeDescription(value: string): string {
  return value.replace(/\r\n?/g, '\n').trim()
}

function validateName(value: string): string {
  const normalized = normalizeName(value)
  return normalized && runeLength(normalized) <= 100
    ? ''
    : t('selfServiceGroups.errors.invalidName')
}

function validateDescription(value: string): string {
  return runeLength(normalizeDescription(value)) <= 2000
    ? ''
    : t('selfServiceGroups.errors.invalidDescription')
}

function resetCreateForm(): void {
  selectedPlatformID.value = platforms.value[0]?.id ?? ''
  createName.value = ''
  createDescription.value = ''
  createNameError.value = ''
  createDescriptionError.value = ''
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
  createNameError.value = validateName(createName.value)
  createDescriptionError.value = validateDescription(createDescription.value)
  return !createNameError.value && !createDescriptionError.value && !!selectedPlatformID.value
}

async function submitCreate(): Promise<void> {
  if (creating.value || !validateCreateForm()) return
  creating.value = true
  createError.value = ''
  try {
    await selfServiceGroupsAPI.create({
      name: normalizeName(createName.value),
      description: normalizeDescription(createDescription.value),
      platform_id: selectedPlatformID.value,
    })
    showCreateDialog.value = false
    resetCreateForm()
    appStore.showSuccess(t('selfServiceGroups.create.success'))
    page.value = 1
    await loadGroups()
  } catch (error) {
    createError.value = groupError(error, 'selfServiceGroups.errors.create')
    if (extractApiErrorCode(error) === 'SELF_SERVICE_GROUP_PLATFORM_UNAVAILABLE') {
      await loadPlatforms()
    }
  } finally {
    creating.value = false
  }
}

function replaceGroup(updated: SelfServiceGroup): void {
  const index = groups.value.findIndex((group) => group.id === updated.id)
  if (index >= 0) groups.value.splice(index, 1, updated)
}

function openGroupDetail(group: SelfServiceGroup): void {
  showDetailDialog.value = true
  detailGroup.value = group
  detailName.value = group.name
  detailDescription.value = group.description
  detailNameError.value = ''
  detailDescriptionError.value = ''
  detailSaveError.value = ''
  void reloadGroupDetail()
}

async function reloadGroupDetail(): Promise<void> {
  const groupID = detailGroup.value?.id
  if (!groupID) return
  const requestVersion = ++detailRequestVersion
  detailLoading.value = true
  detailError.value = ''
  try {
    const group = await selfServiceGroupsAPI.getById(groupID)
    if (requestVersion !== detailRequestVersion || !showDetailDialog.value) return
    detailGroup.value = group
    detailName.value = group.name
    detailDescription.value = group.description
    replaceGroup(group)
  } catch (error) {
    if (requestVersion !== detailRequestVersion || !showDetailDialog.value) return
    detailError.value = groupError(error, 'selfServiceGroups.errors.loadDetail')
  } finally {
    if (requestVersion === detailRequestVersion) detailLoading.value = false
  }
}

function closeDetailDialog(): void {
  if (detailSaving.value) return
  detailRequestVersion++
  showDetailDialog.value = false
  detailGroup.value = null
  detailError.value = ''
  detailSaveError.value = ''
  detailNameError.value = ''
  detailDescriptionError.value = ''
}

async function saveGroup(): Promise<void> {
  const current = detailGroup.value
  if (!current?.owned_by_me || detailSaving.value) return
  const name = normalizeName(detailName.value)
  const description = normalizeDescription(detailDescription.value)
  detailNameError.value = validateName(name)
  detailDescriptionError.value = validateDescription(description)
  if (detailNameError.value || detailDescriptionError.value) return

  const request: UpdateSelfServiceGroupRequest = {}
  if (name !== current.name) request.name = name
  if (description !== current.description) request.description = description
  if (Object.keys(request).length === 0) return

  detailSaving.value = true
  detailSaveError.value = ''
  try {
    const updated = await selfServiceGroupsAPI.update(current.id, request)
    detailGroup.value = updated
    detailName.value = updated.name
    detailDescription.value = updated.description
    replaceGroup(updated)
    appStore.showSuccess(t('selfServiceGroups.detail.saveSuccess'))
  } catch (error) {
    detailSaveError.value = groupError(error, 'selfServiceGroups.errors.update')
  } finally {
    detailSaving.value = false
  }
}

function openDeleteDialog(group: SelfServiceGroup): void {
  if (!group.owned_by_me || deleting.value) return
  deleteTarget.value = group
}

function closeDeleteDialog(): void {
  if (deleting.value) return
  deleteTarget.value = null
}

async function deleteGroup(): Promise<void> {
  const target = deleteTarget.value
  if (!target || deleting.value) return
  deleting.value = true
  try {
    await selfServiceGroupsAPI.delete(target.id)
    deleteTarget.value = null
    if (detailGroup.value?.id === target.id) closeDetailDialog()
    appStore.showSuccess(t('selfServiceGroups.delete.success'))
    if (groups.value.length === 1 && page.value > 1) page.value--
    await loadGroups()
  } catch (error) {
    appStore.showError(groupError(error, 'selfServiceGroups.errors.delete'))
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
})
</script>
