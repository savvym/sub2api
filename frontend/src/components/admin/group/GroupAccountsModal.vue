<template>
  <BaseDialog
    :show="dialogVisible"
    :title="dialogTitle"
    width="full"
    :close-on-escape="!submitting"
    @close="requestClose"
  >
    <div v-if="group" class="space-y-4" data-testid="group-accounts-modal">
      <div class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-200 pb-4 dark:border-dark-700">
        <div class="flex min-w-0 flex-wrap items-center gap-2">
          <span class="inline-flex items-center gap-1.5 rounded bg-gray-100 px-2 py-1 text-xs font-medium text-gray-700 dark:bg-dark-700 dark:text-gray-200">
            <PlatformIcon :platform="group.platform" size="xs" />
            {{ t(`admin.groups.platforms.${group.platform}`) }}
          </span>
          <span :class="['badge text-xs', group.status === 'active' ? 'badge-success' : 'badge-danger']">
            {{ t(`admin.accounts.status.${group.status}`) }}
          </span>
        </div>
        <button
          type="button"
          class="btn btn-primary"
          data-testid="create-group-account"
          :disabled="editorLoading"
          @click="openCreateAccount"
        >
          <Icon :name="editorLoading ? 'refresh' : 'plus'" size="sm" :class="['mr-2', editorLoading && 'animate-spin']" />
          {{ t('admin.groups.accountManagement.createAccount') }}
        </button>
      </div>

      <div class="grid grid-cols-2 gap-1 rounded-md bg-gray-100 p-1 dark:bg-dark-700 lg:hidden" role="tablist">
        <button
          type="button"
          role="tab"
          :aria-selected="mobileTab === 'candidates'"
          :class="mobileTabClass('candidates')"
          data-testid="candidates-tab"
          @click="mobileTab = 'candidates'"
        >
          {{ t('admin.groups.accountManagement.available') }}
          <span class="text-xs opacity-70">({{ projectedEligibleTotal }})</span>
        </button>
        <button
          type="button"
          role="tab"
          :aria-selected="mobileTab === 'members'"
          :class="mobileTabClass('members')"
          data-testid="members-tab"
          @click="mobileTab = 'members'"
        >
          {{ t('admin.groups.accountManagement.current') }}
          <span class="text-xs opacity-70">({{ projectedMemberTotal }})</span>
        </button>
      </div>

      <div class="grid min-h-[33rem] items-stretch gap-3 lg:grid-cols-[minmax(0,1fr)_3rem_minmax(0,1fr)]">
        <section
          :class="[
            panelClass,
            mobileTab !== 'candidates' && 'hidden lg:flex'
          ]"
          aria-labelledby="candidate-panel-title"
        >
          <header class="flex min-h-10 items-center justify-between gap-3 px-4 py-3">
            <div class="min-w-0">
              <h4 id="candidate-panel-title" class="truncate text-sm font-semibold text-gray-900 dark:text-white">
                {{ t('admin.groups.accountManagement.available') }}
              </h4>
              <p class="text-xs text-gray-500 dark:text-gray-400">
                {{ t('admin.groups.accountManagement.eligibleCount', { count: projectedEligibleTotal }) }}
              </p>
            </div>
            <span class="whitespace-nowrap text-xs text-gray-500 dark:text-gray-400">
              {{ t('admin.groups.accountManagement.filteredCount', { count: candidates.total }) }}
            </span>
          </header>

          <div class="grid gap-2 border-y border-gray-200 bg-gray-50 p-3 dark:border-dark-700 dark:bg-dark-800/60 sm:grid-cols-2 xl:grid-cols-3">
            <label class="relative sm:col-span-2 xl:col-span-1">
              <span class="sr-only">{{ t('admin.groups.accountManagement.searchPlaceholder') }}</span>
              <Icon name="search" size="sm" class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-gray-400" />
              <input
                v-model="candidates.search"
                type="search"
                class="input pl-9"
                :placeholder="t('admin.groups.accountManagement.searchPlaceholder')"
                data-testid="candidate-search"
                @input="scheduleCandidateSearch"
              />
            </label>
            <Select
              v-model="candidates.type"
              :options="typeOptions"
              :aria-label="t('admin.groups.accountManagement.allTypes')"
              @change="candidateFiltersChanged"
            />
            <Select
              v-model="candidates.status"
              :options="statusOptions"
              :aria-label="t('admin.groups.accountManagement.allStatuses')"
              @change="candidateFiltersChanged"
            />
            <Select
              v-if="group.platform === 'composite'"
              v-model="candidates.platform"
              :options="platformOptions"
              :aria-label="t('admin.groups.allPlatforms')"
              @change="candidateFiltersChanged"
            />
          </div>

          <div v-if="pendingRemoveRows.length" class="border-b border-amber-200 bg-amber-50/70 p-2 dark:border-amber-900/50 dark:bg-amber-950/20">
            <p class="mb-2 px-1 text-xs font-semibold text-amber-700 dark:text-amber-300">
              {{ t('admin.groups.accountManagement.pendingRemove', { count: pendingRemoveRows.length }) }}
            </p>
            <div class="space-y-1">
              <article
                v-for="account in pendingRemoveRows"
                :key="`remove-${account.id}`"
                class="flex min-h-16 items-center gap-3 rounded border border-amber-200 bg-white p-2.5 dark:border-amber-900/60 dark:bg-dark-800"
                :data-testid="`candidate-row-${account.id}`"
              >
                <input
                  type="checkbox"
                  class="h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500"
                  :aria-label="account.name"
                  :checked="selectedCandidates.has(account.id)"
                  @change="toggleSelection('candidates', account.id, ($event.target as HTMLInputElement).checked)"
                />
                <AccountSummary :account="account" class="min-w-0 flex-1" />
                <span class="badge badge-warning text-[11px]">{{ t('admin.groups.accountManagement.pendingRemoveBadge') }}</span>
                <button
                  type="button"
                  class="rounded p-2 text-gray-500 hover:bg-gray-100 hover:text-primary-600 dark:hover:bg-dark-700"
                  :title="t('admin.groups.accountManagement.undoRemove')"
                  :aria-label="t('admin.groups.accountManagement.undoRemove')"
                  @click="addCandidate(account)"
                >
                  <Icon name="arrowRight" size="sm" />
                </button>
              </article>
            </div>
          </div>

          <div class="flex items-center justify-between border-b border-gray-200 px-3 py-2 dark:border-dark-700">
            <label class="flex items-center gap-2 text-xs font-medium text-gray-600 dark:text-gray-300">
              <input
                type="checkbox"
                class="h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500"
                :checked="allCandidatePageSelected"
                :disabled="candidateRows.length === 0"
                @change="selectCandidatePage(($event.target as HTMLInputElement).checked)"
              />
              {{ t('admin.groups.accountManagement.selectCurrentPage') }}
            </label>
          </div>

          <div class="relative min-h-64 flex-1 overflow-y-auto p-2">
            <div v-if="candidates.loading" class="absolute inset-0 z-10 flex items-center justify-center bg-white/75 dark:bg-dark-800/75">
              <Icon name="refresh" size="lg" class="animate-spin text-primary-500" />
            </div>
            <div v-if="candidates.error" class="flex min-h-56 flex-col items-center justify-center gap-3 px-4 text-center">
              <p class="text-sm text-red-600 dark:text-red-400">{{ candidates.error }}</p>
              <button type="button" class="btn btn-secondary btn-sm" @click="loadCandidates">
                <Icon name="refresh" size="sm" class="mr-1.5" />
                {{ t('admin.groups.accountManagement.retry') }}
              </button>
            </div>
            <div v-else-if="!candidates.loading && candidateRows.length === 0" class="flex min-h-56 items-center justify-center px-4 text-center text-sm text-gray-500 dark:text-gray-400">
              {{ candidateEmptyMessage }}
            </div>
            <div v-else class="space-y-1">
              <article
                v-for="account in candidateRows"
                :key="account.id"
                class="flex min-h-16 items-center gap-3 rounded border border-transparent p-2.5 hover:border-gray-200 hover:bg-gray-50 dark:hover:border-dark-600 dark:hover:bg-dark-700/50"
                :data-testid="`candidate-row-${account.id}`"
              >
                <input
                  type="checkbox"
                  class="h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500"
                  :aria-label="account.name"
                  :checked="selectedCandidates.has(account.id)"
                  @change="toggleSelection('candidates', account.id, ($event.target as HTMLInputElement).checked)"
                />
                <AccountSummary :account="account" class="min-w-0 flex-1" />
                <button
                  type="button"
                  class="rounded p-2 text-gray-500 hover:bg-gray-100 hover:text-primary-600 dark:hover:bg-dark-700 lg:hidden"
                  :title="t('admin.groups.accountManagement.addAccount')"
                  :aria-label="t('admin.groups.accountManagement.addAccount')"
                  :data-testid="`add-account-${account.id}`"
                  @click="addCandidate(account)"
                >
                  <Icon name="arrowRight" size="sm" />
                </button>
              </article>
            </div>
          </div>

          <Pagination
            v-if="candidates.total > 0"
            data-testid="candidate-pagination"
            :page="candidates.page"
            :total="candidates.total"
            :page-size="candidates.pageSize"
            @update:page="changeCandidatePage"
            @update:page-size="changeCandidatePageSize"
          />

          <div v-if="selectedCandidates.size" class="sticky bottom-0 border-t border-gray-200 bg-white p-3 dark:border-dark-700 dark:bg-dark-800 lg:hidden">
            <button type="button" class="btn btn-primary w-full" @click="moveSelectedCandidates">
              <Icon name="arrowRight" size="sm" class="mr-2" />
              {{ t('admin.groups.accountManagement.addSelected') }} ({{ selectedCandidates.size }})
            </button>
          </div>
        </section>

        <div class="hidden flex-col items-center justify-center gap-3 lg:flex">
          <button
            type="button"
            class="flex h-10 w-10 items-center justify-center rounded-md border border-gray-300 bg-white text-gray-600 shadow-sm hover:border-primary-400 hover:text-primary-600 disabled:cursor-not-allowed disabled:opacity-40 dark:border-dark-600 dark:bg-dark-800 dark:text-gray-300"
            :disabled="selectedCandidates.size === 0"
            :title="t('admin.groups.accountManagement.addSelected')"
            :aria-label="t('admin.groups.accountManagement.addSelected')"
            data-testid="move-candidates"
            @click="moveSelectedCandidates"
          >
            <Icon name="arrowRight" size="md" />
          </button>
          <button
            type="button"
            class="flex h-10 w-10 items-center justify-center rounded-md border border-gray-300 bg-white text-gray-600 shadow-sm hover:border-primary-400 hover:text-primary-600 disabled:cursor-not-allowed disabled:opacity-40 dark:border-dark-600 dark:bg-dark-800 dark:text-gray-300"
            :disabled="selectedMembers.size === 0"
            :title="t('admin.groups.accountManagement.removeSelected')"
            :aria-label="t('admin.groups.accountManagement.removeSelected')"
            data-testid="move-members"
            @click="moveSelectedMembers"
          >
            <Icon name="arrowLeft" size="md" />
          </button>
        </div>

        <section
          :class="[
            panelClass,
            mobileTab !== 'members' && 'hidden lg:flex'
          ]"
          aria-labelledby="member-panel-title"
        >
          <header class="flex min-h-10 items-center justify-between gap-3 px-4 py-3">
            <div class="min-w-0">
              <h4 id="member-panel-title" class="truncate text-sm font-semibold text-gray-900 dark:text-white">
                {{ t('admin.groups.accountManagement.current') }}
              </h4>
              <p class="text-xs text-gray-500 dark:text-gray-400">
                {{ t('admin.groups.accountManagement.expectedCount', { count: projectedMemberTotal }) }}
              </p>
            </div>
            <span class="whitespace-nowrap text-xs text-gray-500 dark:text-gray-400">
              {{ t('admin.groups.accountManagement.filteredCount', { count: members.total }) }}
            </span>
          </header>

          <div class="grid gap-2 border-y border-gray-200 bg-gray-50 p-3 dark:border-dark-700 dark:bg-dark-800/60 sm:grid-cols-2 xl:grid-cols-3">
            <label class="relative sm:col-span-2 xl:col-span-1">
              <span class="sr-only">{{ t('admin.groups.accountManagement.searchPlaceholder') }}</span>
              <Icon name="search" size="sm" class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-gray-400" />
              <input
                v-model="members.search"
                type="search"
                class="input pl-9"
                :placeholder="t('admin.groups.accountManagement.searchPlaceholder')"
                data-testid="member-search"
                @input="scheduleMemberSearch"
              />
            </label>
            <Select
              v-model="members.type"
              :options="typeOptions"
              :aria-label="t('admin.groups.accountManagement.allTypes')"
              @change="memberFiltersChanged"
            />
            <Select
              v-model="members.status"
              :options="statusOptions"
              :aria-label="t('admin.groups.accountManagement.allStatuses')"
              @change="memberFiltersChanged"
            />
            <Select
              v-if="group.platform === 'composite'"
              v-model="members.platform"
              :options="platformOptions"
              :aria-label="t('admin.groups.allPlatforms')"
              @change="memberFiltersChanged"
            />
          </div>

          <div v-if="pendingAddRows.length" class="border-b border-emerald-200 bg-emerald-50/70 p-2 dark:border-emerald-900/50 dark:bg-emerald-950/20">
            <p class="mb-2 px-1 text-xs font-semibold text-emerald-700 dark:text-emerald-300">
              {{ t('admin.groups.accountManagement.pendingAdd', { count: pendingAddRows.length }) }}
            </p>
            <div class="space-y-1">
              <article
                v-for="account in pendingAddRows"
                :key="`add-${account.id}`"
                class="flex min-h-16 items-center gap-3 rounded border border-emerald-200 bg-white p-2.5 dark:border-emerald-900/60 dark:bg-dark-800"
                :data-testid="`member-row-${account.id}`"
              >
                <input
                  type="checkbox"
                  class="h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500"
                  :aria-label="account.name"
                  :checked="selectedMembers.has(account.id)"
                  @change="toggleSelection('members', account.id, ($event.target as HTMLInputElement).checked)"
                />
                <AccountSummary :account="account" class="min-w-0 flex-1" />
                <span class="badge badge-success text-[11px]">{{ t('admin.groups.accountManagement.pendingAddBadge') }}</span>
                <button
                  type="button"
                  class="rounded p-2 text-gray-500 hover:bg-gray-100 hover:text-primary-600 dark:hover:bg-dark-700"
                  :title="t('admin.groups.accountManagement.editAccount')"
                  :aria-label="t('admin.groups.accountManagement.editAccount')"
                  :disabled="editorLoading"
                  @click="openEditAccount(account.id)"
                >
                  <Icon name="edit" size="sm" />
                </button>
                <button
                  type="button"
                  class="rounded p-2 text-gray-500 hover:bg-gray-100 hover:text-primary-600 dark:hover:bg-dark-700"
                  :title="t('admin.groups.accountManagement.undoAdd')"
                  :aria-label="t('admin.groups.accountManagement.undoAdd')"
                  @click="removeMember(account)"
                >
                  <Icon name="arrowLeft" size="sm" />
                </button>
              </article>
            </div>
          </div>

          <div class="flex items-center justify-between border-b border-gray-200 px-3 py-2 dark:border-dark-700">
            <label class="flex items-center gap-2 text-xs font-medium text-gray-600 dark:text-gray-300">
              <input
                type="checkbox"
                class="h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500"
                :checked="allMemberPageSelected"
                :disabled="memberRows.length === 0"
                @change="selectMemberPage(($event.target as HTMLInputElement).checked)"
              />
              {{ t('admin.groups.accountManagement.selectCurrentPage') }}
            </label>
          </div>

          <div class="relative min-h-64 flex-1 overflow-y-auto p-2">
            <div v-if="members.loading" class="absolute inset-0 z-10 flex items-center justify-center bg-white/75 dark:bg-dark-800/75">
              <Icon name="refresh" size="lg" class="animate-spin text-primary-500" />
            </div>
            <div v-if="members.error" class="flex min-h-56 flex-col items-center justify-center gap-3 px-4 text-center">
              <p class="text-sm text-red-600 dark:text-red-400">{{ members.error }}</p>
              <button type="button" class="btn btn-secondary btn-sm" data-testid="retry-members" @click="loadMembers">
                <Icon name="refresh" size="sm" class="mr-1.5" />
                {{ t('admin.groups.accountManagement.retry') }}
              </button>
            </div>
            <div v-else-if="!members.loading && memberRows.length === 0" class="flex min-h-56 items-center justify-center px-4 text-center text-sm text-gray-500 dark:text-gray-400">
              {{ memberEmptyMessage }}
            </div>
            <div v-else class="space-y-1">
              <article
                v-for="account in memberRows"
                :key="account.id"
                class="flex min-h-16 items-center gap-3 rounded border border-transparent p-2.5 hover:border-gray-200 hover:bg-gray-50 dark:hover:border-dark-600 dark:hover:bg-dark-700/50"
                :data-testid="`member-row-${account.id}`"
              >
                <input
                  type="checkbox"
                  class="h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500"
                  :aria-label="account.name"
                  :checked="selectedMembers.has(account.id)"
                  @change="toggleSelection('members', account.id, ($event.target as HTMLInputElement).checked)"
                />
                <AccountSummary :account="account" class="min-w-0 flex-1" />
                <button
                  type="button"
                  class="rounded p-2 text-gray-500 hover:bg-gray-100 hover:text-primary-600 dark:hover:bg-dark-700"
                  :title="t('admin.groups.accountManagement.editAccount')"
                  :aria-label="t('admin.groups.accountManagement.editAccount')"
                  :data-testid="`edit-account-${account.id}`"
                  :disabled="editorLoading"
                  @click="openEditAccount(account.id)"
                >
                  <Icon name="edit" size="sm" />
                </button>
                <button
                  type="button"
                  class="rounded p-2 text-gray-500 hover:bg-gray-100 hover:text-primary-600 dark:hover:bg-dark-700 lg:hidden"
                  :title="t('admin.groups.accountManagement.removeAccount')"
                  :aria-label="t('admin.groups.accountManagement.removeAccount')"
                  :data-testid="`remove-account-${account.id}`"
                  @click="removeMember(account)"
                >
                  <Icon name="arrowLeft" size="sm" />
                </button>
              </article>
            </div>
          </div>

          <Pagination
            v-if="members.total > 0"
            data-testid="member-pagination"
            :page="members.page"
            :total="members.total"
            :page-size="members.pageSize"
            @update:page="changeMemberPage"
            @update:page-size="changeMemberPageSize"
          />

          <div v-if="selectedMembers.size" class="sticky bottom-0 border-t border-gray-200 bg-white p-3 dark:border-dark-700 dark:bg-dark-800 lg:hidden">
            <button type="button" class="btn btn-secondary w-full" @click="moveSelectedMembers">
              <Icon name="arrowLeft" size="sm" class="mr-2" />
              {{ t('admin.groups.accountManagement.removeSelected') }} ({{ selectedMembers.size }})
            </button>
          </div>
        </section>
      </div>

      <div v-if="saveError" class="rounded-md border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700 dark:border-red-900/50 dark:bg-red-950/20 dark:text-red-300" role="alert">
        {{ saveError }}
      </div>
    </div>

    <template #footer>
      <div class="flex w-full flex-col-reverse gap-3 sm:flex-row sm:items-center sm:justify-between">
        <p class="text-sm text-gray-600 dark:text-gray-300">
          {{ t('admin.groups.accountManagement.saveSummary', { add: pendingAdd.size, remove: pendingRemove.size }) }}
        </p>
        <div class="flex justify-end gap-3">
          <button type="button" class="btn btn-secondary" data-testid="group-accounts-cancel" :disabled="submitting" @click="requestClose">
            {{ t('common.cancel') }}
          </button>
          <button
            type="button"
            class="btn btn-primary"
            data-testid="save-group-accounts"
            :disabled="!canSave"
            @click="saveChanges()"
          >
            <Icon v-if="submitting" name="refresh" size="sm" class="mr-2 animate-spin" />
            {{ submitting ? t('common.saving') : t('admin.groups.accountManagement.saveChanges') }}
          </button>
        </div>
      </div>
    </template>
  </BaseDialog>

  <ConfirmDialog
    :show="showDiscardConfirm"
    :title="t('admin.groups.accountManagement.discardTitle')"
    :message="t('admin.groups.accountManagement.discardMessage')"
    :confirm-text="t('common.confirm')"
    :cancel-text="t('common.cancel')"
    :danger="true"
    @confirm="discardAndClose"
    @cancel="showDiscardConfirm = false"
  />

  <ConfirmDialog
    :show="showRiskConfirm"
    :title="t('admin.groups.accountManagement.riskTitle')"
    :message="t('admin.groups.accountManagement.riskMessage')"
    :confirm-text="t('admin.groups.accountManagement.riskConfirm')"
    :cancel-text="t('common.cancel')"
    @confirm="confirmRiskAndSave"
    @cancel="cancelRiskConfirm"
  />

  <CreateAccountModal
    v-if="showCreateAccountEditor"
    :show="showCreateAccountEditor"
    :proxies="editorProxies"
    :groups="editorGroups"
    :initial-platform="contextPlatform"
    :lock-platform="contextPlatform != null"
    :required-group-id="group?.id"
    @close="showCreateAccountEditor = false"
    @created="handleAccountCreated"
  />

  <EditAccountModal
    v-if="showEditAccountEditor"
    :show="showEditAccountEditor"
    :account="editingAccount"
    :proxies="editorProxies"
    :groups="editorGroups"
    :preserve-group-membership="true"
    @close="closeEditAccount"
    @updated="handleAccountUpdated"
  />
</template>

<script setup lang="ts">
import { computed, defineComponent, h, onBeforeUnmount, reactive, ref, watch, type PropType } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import { useAppStore } from '@/stores/app'
import { extractApiErrorCode, extractApiErrorMessage, extractI18nErrorMessage } from '@/utils/apiError'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Pagination from '@/components/common/Pagination.vue'
import Select from '@/components/common/Select.vue'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import Icon from '@/components/icons/Icon.vue'
import { CreateAccountModal, EditAccountModal } from '@/components/account'
import type {
  Account,
  AccountPlatform,
  AccountType,
  AdminGroup,
  GroupAccountMembershipChangeResult,
  GroupAccountSummary,
  Proxy
} from '@/types'

type Side = 'candidates' | 'members'

interface ListState {
  items: GroupAccountSummary[]
  total: number
  globalTotal: number
  page: number
  pageSize: number
  search: string
  type: AccountType | ''
  status: 'active' | 'inactive' | 'error' | ''
  platform: AccountPlatform | ''
  loading: boolean
  loaded: boolean
  error: string
}

const props = defineProps<{
  show: boolean
  group: AdminGroup | null
}>()

const emit = defineEmits<{
  close: []
  success: [result: GroupAccountMembershipChangeResult]
  refresh: []
  createAccount: [group: AdminGroup]
  editAccount: [accountId: number]
}>()

const { t, te } = useI18n()
const appStore = useAppStore()

const makeListState = (): ListState => ({
  items: [],
  total: 0,
  globalTotal: 0,
  page: 1,
  pageSize: 20,
  search: '',
  type: '',
  status: '',
  platform: '',
  loading: false,
  loaded: false,
  error: ''
})

const candidates = reactive<ListState>(makeListState())
const members = reactive<ListState>(makeListState())
const pendingAdd = ref(new Map<number, GroupAccountSummary>())
const pendingRemove = ref(new Map<number, GroupAccountSummary>())
const recentMembers = ref(new Map<number, GroupAccountSummary>())
const selectedCandidates = ref(new Set<number>())
const selectedMembers = ref(new Set<number>())
const mobileTab = ref<Side>('candidates')
const submitting = ref(false)
const saveError = ref('')
const showDiscardConfirm = ref(false)
const showRiskConfirm = ref(false)
const riskConfirmationToken = ref<string | null>(null)
const editorLoading = ref(false)
const showCreateAccountEditor = ref(false)
const showEditAccountEditor = ref(false)
const editingAccount = ref<Account | null>(null)
const editorProxies = ref<Proxy[]>([])
const editorGroups = ref<AdminGroup[]>([])
let editorOptionsPromise: Promise<void> | null = null
let saveOperation: { fingerprint: string; key: string } | null = null
let candidateAbortController: AbortController | null = null
let memberAbortController: AbortController | null = null
let candidateSearchTimer: ReturnType<typeof setTimeout> | null = null
let memberSearchTimer: ReturnType<typeof setTimeout> | null = null
let missingGroupHandled = false

const panelClass = 'min-w-0 flex flex-col overflow-hidden rounded-md border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-800'
const dialogVisible = computed(
  () =>
    props.show &&
    !showDiscardConfirm.value &&
    !showRiskConfirm.value &&
    !showCreateAccountEditor.value &&
    !showEditAccountEditor.value
)
const contextPlatform = computed<AccountPlatform | undefined>(() =>
  props.group && props.group.platform !== 'composite' ? props.group.platform : undefined
)
const dialogTitle = computed(() =>
  props.group
    ? t('admin.groups.accountManagement.title', { name: props.group.name })
    : t('admin.groups.accountManagement.action')
)
const hasChanges = computed(() => pendingAdd.value.size + pendingRemove.value.size > 0)
const canSave = computed(
  () => hasChanges.value && members.loaded && !members.error && !submitting.value
)
const projectedEligibleTotal = computed(() =>
  Math.max(0, candidates.globalTotal - pendingAdd.value.size)
)
const projectedMemberTotal = computed(() =>
  Math.max(0, members.globalTotal + pendingAdd.value.size - pendingRemove.value.size)
)
const pendingAddRows = computed(() => [...pendingAdd.value.values()])
const pendingRemoveRows = computed(() => [...pendingRemove.value.values()])
const candidateRows = computed(() =>
  candidates.items.filter(
    (account) => !pendingAdd.value.has(account.id) && !pendingRemove.value.has(account.id)
  )
)
const memberRows = computed(() =>
  [
    ...recentMembers.value.values(),
    ...members.items.filter((account) => !recentMembers.value.has(account.id))
  ].filter((account) => !pendingRemove.value.has(account.id) && !pendingAdd.value.has(account.id))
)
const allCandidatePageSelected = computed(
  () => candidateRows.value.length > 0 && candidateRows.value.every((account) => selectedCandidates.value.has(account.id))
)
const allMemberPageSelected = computed(
  () => memberRows.value.length > 0 && memberRows.value.every((account) => selectedMembers.value.has(account.id))
)
const candidateEmptyMessage = computed(() =>
  candidates.globalTotal > 0
    ? t('admin.groups.accountManagement.emptyCandidatesFiltered')
    : t('admin.groups.accountManagement.emptyCandidatesEligible')
)
const memberEmptyMessage = computed(() =>
  members.globalTotal > 0
    ? t('admin.groups.accountManagement.emptyMembersFiltered')
    : t('admin.groups.accountManagement.emptyMembers')
)

const typeOptions = computed(() => [
  { value: '', label: t('admin.groups.accountManagement.allTypes') },
  ...(['oauth', 'setup-token', 'apikey', 'upstream', 'bedrock', 'service_account'] as AccountType[]).map((type) => ({
    value: type,
    label: t(`admin.groups.accountManagement.types.${type}`)
  }))
])
const statusOptions = computed(() => [
  { value: '', label: t('admin.groups.accountManagement.allStatuses') },
  { value: 'active', label: t('admin.accounts.status.active') },
  { value: 'inactive', label: t('admin.accounts.status.inactive') },
  { value: 'error', label: t('admin.accounts.status.error') }
])
const platformOptions = computed(() => [
  { value: '', label: t('admin.groups.allPlatforms') },
  ...(['anthropic', 'openai', 'gemini', 'antigravity', 'grok', 'kimi', 'zhipu', 'deepseek'] as AccountPlatform[]).map((platform) => ({
    value: platform,
    label: t(`admin.groups.platforms.${platform}`)
  }))
])

const AccountSummary = defineComponent({
  name: 'GroupAccountSummary',
  props: {
    account: {
      type: Object as PropType<GroupAccountSummary>,
      required: true
    }
  },
  setup(summaryProps) {
    const isFuture = (value?: string | null) => {
      if (!value) return false
      const timestamp = Date.parse(value)
      return Number.isFinite(timestamp) && timestamp > Date.now()
    }
    const schedulingKey = computed(() => {
      if (isFuture(summaryProps.account.rate_limit_reset_at)) return 'rateLimited'
      if (
        isFuture(summaryProps.account.overload_until) ||
        isFuture(summaryProps.account.temp_unschedulable_until)
      ) return 'temporary'
      return summaryProps.account.schedulable ? 'schedulable' : 'unschedulable'
    })
    const schedulingClass = computed(() => {
      if (schedulingKey.value === 'schedulable') return 'text-emerald-600 dark:text-emerald-400'
      if (schedulingKey.value === 'rateLimited') return 'text-amber-600 dark:text-amber-400'
      return 'text-red-600 dark:text-red-400'
    })
    const accountStatusClass = computed(() => {
      if (summaryProps.account.status === 'active') return 'text-emerald-600 dark:text-emerald-400'
      if (summaryProps.account.status === 'error') return 'text-red-600 dark:text-red-400'
      return 'text-gray-500 dark:text-gray-400'
    })
    const warningText = (warning: string) => {
      const key = `admin.groups.accountManagement.policyWarnings.${warning}`
      return te(key) ? t(key) : warning
    }

    return () => h('div', { class: 'min-w-0' }, [
      h('div', { class: 'flex min-w-0 items-center gap-2' }, [
        h('span', { class: 'truncate text-sm font-medium text-gray-900 dark:text-white', title: summaryProps.account.name }, summaryProps.account.name),
        h('span', { class: 'shrink-0 font-mono text-[11px] text-gray-400' }, `#${summaryProps.account.id}`)
      ]),
      h('div', { class: 'mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 text-[11px] text-gray-500 dark:text-gray-400' }, [
        h('span', `${t(`admin.groups.platforms.${summaryProps.account.platform}`)} · ${t(`admin.groups.accountManagement.types.${summaryProps.account.type}`)}`),
        h('span', { class: accountStatusClass.value }, t(`admin.accounts.status.${summaryProps.account.status}`)),
        h('span', { class: schedulingClass.value }, t(`admin.groups.accountManagement.scheduling.${schedulingKey.value}`)),
        h('span', t('admin.groups.accountManagement.accountGroups', { count: summaryProps.account.group_count }))
      ]),
      ...(summaryProps.account.policy_warnings || []).map((warning) =>
        h('p', { class: 'mt-1 truncate text-[11px] text-amber-600 dark:text-amber-400', title: warningText(warning) }, warningText(warning))
      )
    ])
  }
})

function mobileTabClass(tab: Side): string[] {
  return [
    'rounded px-3 py-2 text-sm font-medium transition-colors',
    mobileTab.value === tab
      ? 'bg-white text-gray-900 shadow-sm dark:bg-dark-800 dark:text-white'
      : 'text-gray-500 hover:text-gray-800 dark:text-gray-400 dark:hover:text-gray-200'
  ]
}

function clearTimer(timer: ReturnType<typeof setTimeout> | null): void {
  if (timer) clearTimeout(timer)
}

function resetListState(state: ListState): void {
  const pageSize = state.pageSize
  Object.assign(state, makeListState(), { pageSize })
}

function resetState(): void {
  candidateAbortController?.abort()
  memberAbortController?.abort()
  clearTimer(candidateSearchTimer)
  clearTimer(memberSearchTimer)
  resetListState(candidates)
  resetListState(members)
  pendingAdd.value = new Map()
  pendingRemove.value = new Map()
  recentMembers.value = new Map()
  selectedCandidates.value = new Set()
  selectedMembers.value = new Set()
  mobileTab.value = 'candidates'
  submitting.value = false
  saveError.value = ''
  showDiscardConfirm.value = false
  showRiskConfirm.value = false
  riskConfirmationToken.value = null
  editorLoading.value = false
  showCreateAccountEditor.value = false
  showEditAccountEditor.value = false
  editingAccount.value = null
  editorProxies.value = []
  editorGroups.value = []
  editorOptionsPromise = null
  missingGroupHandled = false
  saveOperation = null
}

function queryFor(state: ListState) {
  return {
    search: state.search.trim() || undefined,
    type: state.type || undefined,
    status: state.status || undefined,
    platform: state.platform || undefined
  }
}

function isCancelled(error: unknown): boolean {
  return !!error && typeof error === 'object' && (error as { code?: string }).code === 'ERR_CANCELED'
}

function handleMissingGroup(error: unknown): boolean {
  if ((extractApiErrorCode(error) || '').toLowerCase() !== 'group_not_found') return false
  if (!missingGroupHandled) {
    missingGroupHandled = true
    appStore.showError(t('admin.groups.accountManagement.errors.group_not_found'))
    emit('close')
  }
  return true
}

function summaryFromAccount(account: Account, fallback?: GroupAccountSummary): GroupAccountSummary {
  return {
    id: account.id,
    name: account.name,
    platform: account.platform,
    type: account.type,
    status: account.status,
    schedulable: account.schedulable ?? fallback?.schedulable ?? false,
    rate_limited_at: account.rate_limited_at ?? fallback?.rate_limited_at ?? null,
    rate_limit_reset_at: account.rate_limit_reset_at ?? fallback?.rate_limit_reset_at ?? null,
    overload_until: account.overload_until ?? fallback?.overload_until ?? null,
    temp_unschedulable_until:
      account.temp_unschedulable_until ?? fallback?.temp_unschedulable_until ?? null,
    group_count: account.group_ids?.length ?? account.groups?.length ?? fallback?.group_count ?? 1,
    policy_warnings: fallback?.policy_warnings ?? []
  }
}

function replaceMappedSummary(
  source: typeof pendingAdd,
  summary: GroupAccountSummary
): void {
  if (!source.value.has(summary.id)) return
  const next = new Map(source.value)
  next.set(summary.id, summary)
  source.value = next
}

function updatePinnedAccount(account: Account): void {
  const fallback =
    pendingAdd.value.get(account.id) ||
    pendingRemove.value.get(account.id) ||
    recentMembers.value.get(account.id) ||
    candidates.items.find((item) => item.id === account.id) ||
    members.items.find((item) => item.id === account.id)
  const summary = summaryFromAccount(account, fallback)
  replaceMappedSummary(pendingAdd, summary)
  replaceMappedSummary(pendingRemove, summary)
  replaceMappedSummary(recentMembers, summary)
}

function mergeServerSummaries(items: GroupAccountSummary[]): void {
  for (const summary of items) {
    replaceMappedSummary(pendingAdd, summary)
    replaceMappedSummary(pendingRemove, summary)
    replaceMappedSummary(recentMembers, summary)
  }
}

async function loadCandidates(): Promise<void> {
  if (!props.group) return
  candidateAbortController?.abort()
  const controller = new AbortController()
  candidateAbortController = controller
  candidates.loading = true
  candidates.error = ''
  selectedCandidates.value = new Set()
  try {
    const result = await adminAPI.groups.listAccountCandidates(
      props.group.id,
      candidates.page,
      candidates.pageSize,
      queryFor(candidates),
      { signal: controller.signal }
    )
    if (controller.signal.aborted) return
    candidates.items = result.items
    candidates.total = result.total
    candidates.globalTotal = result.eligible_total
    candidates.loaded = true
    mergeServerSummaries(result.items)
  } catch (error) {
    if (isCancelled(error) || handleMissingGroup(error)) return
    candidates.error = extractI18nErrorMessage(
      error,
      t,
      'admin.groups.accountManagement.errors',
      t('admin.groups.accountManagement.loadFailed')
    )
  } finally {
    if (candidateAbortController === controller) {
      candidates.loading = false
      candidateAbortController = null
    }
  }
}

async function loadMembers(): Promise<void> {
  if (!props.group) return
  memberAbortController?.abort()
  const controller = new AbortController()
  memberAbortController = controller
  members.loading = true
  selectedMembers.value = new Set()
  try {
    const result = await adminAPI.groups.listAccounts(
      props.group.id,
      members.page,
      members.pageSize,
      queryFor(members),
      { signal: controller.signal }
    )
    if (controller.signal.aborted) return
    members.items = result.items
    members.total = result.total
    members.globalTotal = result.member_total
    members.loaded = true
    members.error = ''
    mergeServerSummaries(result.items)
  } catch (error) {
    if (isCancelled(error) || handleMissingGroup(error)) return
    members.error = extractI18nErrorMessage(
      error,
      t,
      'admin.groups.accountManagement.errors',
      t('admin.groups.accountManagement.loadFailed')
    )
  } finally {
    if (memberAbortController === controller) {
      members.loading = false
      memberAbortController = null
    }
  }
}

async function loadBoth(): Promise<void> {
  await Promise.allSettled([loadCandidates(), loadMembers()])
}

function scheduleCandidateSearch(): void {
  clearTimer(candidateSearchTimer)
  candidateSearchTimer = setTimeout(() => {
    candidates.page = 1
    void loadCandidates()
  }, 300)
}

function scheduleMemberSearch(): void {
  clearTimer(memberSearchTimer)
  memberSearchTimer = setTimeout(() => {
    members.page = 1
    void loadMembers()
  }, 300)
}

function candidateFiltersChanged(): void {
  candidates.page = 1
  void loadCandidates()
}

function memberFiltersChanged(): void {
  members.page = 1
  void loadMembers()
}

function changeCandidatePage(page: number): void {
  candidates.page = page
  void loadCandidates()
}

function changeMemberPage(page: number): void {
  members.page = page
  void loadMembers()
}

function changeCandidatePageSize(pageSize: number): void {
  candidates.page = 1
  candidates.pageSize = pageSize
  void loadCandidates()
}

function changeMemberPageSize(pageSize: number): void {
  members.page = 1
  members.pageSize = pageSize
  void loadMembers()
}

function toggleSelection(side: Side, id: number, selected: boolean): void {
  const source = side === 'candidates' ? selectedCandidates : selectedMembers
  const next = new Set(source.value)
  if (selected) next.add(id)
  else next.delete(id)
  source.value = next
}

function selectCandidatePage(selected: boolean): void {
  const next = new Set(selectedCandidates.value)
  for (const account of candidateRows.value) {
    if (selected) next.add(account.id)
    else next.delete(account.id)
  }
  selectedCandidates.value = next
}

function selectMemberPage(selected: boolean): void {
  const next = new Set(selectedMembers.value)
  for (const account of memberRows.value) {
    if (selected) next.add(account.id)
    else next.delete(account.id)
  }
  selectedMembers.value = next
}

function resetSaveAttempt(): void {
  saveOperation = null
  riskConfirmationToken.value = null
  showRiskConfirm.value = false
  saveError.value = ''
}

function addCandidate(account: GroupAccountSummary): void {
  const nextAdd = new Map(pendingAdd.value)
  const nextRemove = new Map(pendingRemove.value)
  if (nextRemove.has(account.id)) nextRemove.delete(account.id)
  else nextAdd.set(account.id, account)
  pendingAdd.value = nextAdd
  pendingRemove.value = nextRemove
  selectedCandidates.value = new Set()
  resetSaveAttempt()
}

function removeMember(account: GroupAccountSummary): void {
  const nextAdd = new Map(pendingAdd.value)
  const nextRemove = new Map(pendingRemove.value)
  if (nextAdd.has(account.id)) nextAdd.delete(account.id)
  else nextRemove.set(account.id, account)
  pendingAdd.value = nextAdd
  pendingRemove.value = nextRemove
  selectedMembers.value = new Set()
  resetSaveAttempt()
}

function candidateById(id: number): GroupAccountSummary | undefined {
  return pendingRemove.value.get(id) || candidates.items.find((account) => account.id === id)
}

function memberById(id: number): GroupAccountSummary | undefined {
  return (
    pendingAdd.value.get(id) ||
    recentMembers.value.get(id) ||
    members.items.find((account) => account.id === id)
  )
}

function moveSelectedCandidates(): void {
  for (const id of selectedCandidates.value) {
    const account = candidateById(id)
    if (account) addCandidate(account)
  }
  selectedCandidates.value = new Set()
}

function moveSelectedMembers(): void {
  for (const id of selectedMembers.value) {
    const account = memberById(id)
    if (account) removeMember(account)
  }
  selectedMembers.value = new Set()
}

function requestClose(): void {
  if (submitting.value) return
  if (hasChanges.value) {
    showDiscardConfirm.value = true
    return
  }
  emit('close')
}

function discardAndClose(): void {
  showDiscardConfirm.value = false
  resetState()
  emit('close')
}

function cancelRiskConfirm(): void {
  showRiskConfirm.value = false
  riskConfirmationToken.value = null
  saveOperation = null
}

function operationKeyFor(fingerprint: string): string {
  if (saveOperation?.fingerprint === fingerprint) return saveOperation.key
  const randomPart = globalThis.crypto?.randomUUID?.() ?? `${Date.now()}-${Math.random().toString(36).slice(2)}`
  const key = `group-accounts-${props.group?.id ?? 'unknown'}-${randomPart}`
  saveOperation = { fingerprint, key }
  return key
}

function errorMetadata(error: unknown): Record<string, unknown> {
  if (!error || typeof error !== 'object') return {}
  return (error as { metadata?: Record<string, unknown> }).metadata || {}
}

function shouldResetSaveOperation(error: unknown): boolean {
  if (!error || typeof error !== 'object') return false
  const reason = (extractApiErrorCode(error) || '').toUpperCase()
  if (reason === 'IDEMPOTENCY_IN_PROGRESS' || reason === 'IDEMPOTENCY_RETRY_BACKOFF') {
    return false
  }
  const status = Number((error as { status?: unknown }).status)
  return Number.isFinite(status) && status >= 400 && status < 500
}

async function saveChanges(confirmationToken?: string | null): Promise<void> {
  if (!props.group || !hasChanges.value || submitting.value) return
  if (!members.loaded || members.error) {
    saveError.value = members.error || t('admin.groups.accountManagement.loadFailed')
    return
  }
  const addIds = [...pendingAdd.value.keys()].sort((a, b) => a - b)
  const removeIds = [...pendingRemove.value.keys()].sort((a, b) => a - b)
  if (addIds.length + removeIds.length > 500) {
    saveError.value = t('admin.groups.accountManagement.diffTooLarge')
    return
  }

  const token = confirmationToken === undefined
    ? riskConfirmationToken.value
    : confirmationToken
  const fingerprint = JSON.stringify({ addIds, removeIds, token })
  submitting.value = true
  saveError.value = ''
  try {
    const result = await adminAPI.groups.updateAccounts(
      props.group.id,
      {
        add_account_ids: addIds,
        remove_account_ids: removeIds,
        risk_confirmation_token: token
      },
      { idempotencyKey: operationKeyFor(fingerprint) }
    )
    pendingAdd.value = new Map()
    pendingRemove.value = new Map()
    selectedCandidates.value = new Set()
    selectedMembers.value = new Set()
    saveOperation = null
    riskConfirmationToken.value = null
    appStore.showSuccess(t('admin.groups.accountManagement.saved'))
    emit('success', result)
    emit('close')
  } catch (error) {
    const reason = extractApiErrorCode(error)
    if (reason === 'mixed_channel_warning') {
      const riskToken = errorMetadata(error).risk_confirmation_token
      if (typeof riskToken === 'string' && riskToken) {
        riskConfirmationToken.value = riskToken
        showRiskConfirm.value = true
        saveOperation = null
        return
      }
    }
    if (handleMissingGroup(error)) return
    if (shouldResetSaveOperation(error)) {
      saveOperation = null
      riskConfirmationToken.value = null
    }
    saveError.value = extractI18nErrorMessage(
      error,
      t,
      'admin.groups.accountManagement.errors',
      extractApiErrorMessage(error, t('admin.groups.failedToSave'))
    )
    appStore.showError(saveError.value)
  } finally {
    submitting.value = false
  }
}

function confirmRiskAndSave(): void {
  const token = riskConfirmationToken.value
  showRiskConfirm.value = false
  if (token) void saveChanges(token)
}

async function loadEditorOptions(): Promise<void> {
  if (editorGroups.value.length > 0 || editorOptionsPromise) {
    return editorOptionsPromise ?? Promise.resolve()
  }
  editorOptionsPromise = Promise.all([
    adminAPI.proxies.getAll(),
    adminAPI.groups.getAllIncludingInactive()
  ]).then(([proxies, groups]) => {
    editorProxies.value = proxies
    editorGroups.value = groups
  }).finally(() => {
    editorOptionsPromise = null
  })
  return editorOptionsPromise
}

async function openCreateAccount(): Promise<void> {
  if (!props.group || editorLoading.value) return
  const groupId = props.group.id
  emit('createAccount', props.group)
  editorLoading.value = true
  try {
    await loadEditorOptions()
    if (!props.show || props.group?.id !== groupId) return
    showCreateAccountEditor.value = true
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.groups.accountManagement.editorLoadFailed')))
  } finally {
    editorLoading.value = false
  }
}

async function openEditAccount(accountId: number): Promise<void> {
  if (!props.group || editorLoading.value) return
  const groupId = props.group.id
  emit('editAccount', accountId)
  editorLoading.value = true
  try {
    const [, account] = await Promise.all([
      loadEditorOptions(),
      adminAPI.accounts.getById(accountId)
    ])
    if (!props.show || props.group?.id !== groupId) return
    editingAccount.value = account
    showEditAccountEditor.value = true
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.groups.accountManagement.editorLoadFailed')))
    await loadBoth()
  } finally {
    editorLoading.value = false
  }
}

function closeEditAccount(): void {
  showEditAccountEditor.value = false
  editingAccount.value = null
}

async function handleAccountCreated(account?: Account): Promise<void> {
  showCreateAccountEditor.value = false
  if (account) {
    const summary = summaryFromAccount(account)
    const next = new Map(recentMembers.value)
    next.set(summary.id, summary)
    recentMembers.value = next
  }
  await loadBoth()
  emit('refresh')
}

async function handleAccountUpdated(account?: Account): Promise<void> {
  closeEditAccount()
  await loadBoth()
  if (account) updatePinnedAccount(account)
  emit('refresh')
}

watch(
  () => [props.show, props.group?.id] as const,
  ([show, groupId], previous) => {
    const [wasShow, oldGroupId] = previous ?? [false, undefined]
    if (show && groupId && (!wasShow || groupId !== oldGroupId)) {
      resetState()
      void loadBoth()
    } else if (!show && wasShow) {
      resetState()
    }
  },
  { immediate: true }
)

onBeforeUnmount(() => {
  candidateAbortController?.abort()
  memberAbortController?.abort()
  clearTimer(candidateSearchTimer)
  clearTimer(memberSearchTimer)
})
</script>

<style scoped>
:deep(.modal-body) {
  @apply px-4 py-4 sm:px-6;
}

section :deep(.page-size-select) {
  @apply hidden xl:block;
}

section :deep(.sm\:flex-1) {
  min-width: 0;
}
</style>
