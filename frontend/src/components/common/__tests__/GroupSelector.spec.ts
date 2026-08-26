import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key })
  }
})

import GroupSelector from '../GroupSelector.vue'

const groups = [
  { id: 7, name: 'Required', platform: 'openai', rate_multiplier: 1, account_count: 2 },
  { id: 8, name: 'Optional', platform: 'openai', rate_multiplier: 1, account_count: 0 }
]

describe('GroupSelector locked groups', () => {
  it('renders required groups as checked and disabled while leaving other groups editable', async () => {
    const wrapper = mount(GroupSelector, {
      props: {
        modelValue: [7],
        groups: groups as any,
        platform: 'openai',
        lockedGroupIds: [7]
      },
      global: {
        stubs: {
          GroupBadge: { template: '<span />' },
          Icon: true
        }
      }
    })

    const inputs = wrapper.findAll('input[type="checkbox"]')
    expect(inputs).toHaveLength(2)
    expect(inputs[0].attributes('disabled')).toBeDefined()
    expect((inputs[0].element as HTMLInputElement).checked).toBe(true)
    expect(inputs[1].attributes('disabled')).toBeUndefined()

    await inputs[1].setValue(true)
    expect(wrapper.emitted('update:modelValue')?.at(-1)).toEqual([[7, 8]])
  })
})
