import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'

import Pagination from '../Pagination.vue'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string, params?: Record<string, number>) => {
      if (key === 'pagination.pageOf') {
        return `Page ${params?.page} of ${params?.total}`
      }
      return key === 'pagination.previous' ? 'Previous' : key === 'pagination.next' ? 'Next' : key
    },
  }),
}))

describe('Pagination', () => {
  it('renders an empty result set as page 1 of 1 with navigation disabled', () => {
    const wrapper = mount(Pagination, {
      props: { total: 0, page: 1, pageSize: 20 },
      global: {
        stubs: {
          Icon: true,
          Select: true,
        },
      },
    })

    expect(wrapper.text()).toContain('Page 1 of 1')

    const navigationButtons = wrapper.findAll('button').filter((button) => {
      const name = button.attributes('aria-label') || button.text()
      return name === 'Previous' || name === 'Next'
    })
    expect(navigationButtons).toHaveLength(4)
    expect(navigationButtons.every((button) => button.attributes('disabled') !== undefined)).toBe(true)
  })
})
