import { afterEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { defineComponent, nextTick } from 'vue'
import BaseDialog from '../BaseDialog.vue'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key })
}))

describe('BaseDialog', () => {
  afterEach(() => {
    document.body.innerHTML = ''
    document.body.classList.remove('modal-open')
  })

  it('resets body scroll position when reopened', async () => {
    const wrapper = mount(BaseDialog, {
      attachTo: document.body,
      props: { show: false, title: 'Details' },
      slots: { default: '<div style="height: 2000px">content</div>' },
      global: { stubs: { Icon: true } }
    })

    await wrapper.setProps({ show: true })
    await nextTick()
    const body = document.body.querySelector<HTMLElement>('.modal-body')
    expect(body).not.toBeNull()
    body!.scrollTop = 480

    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true })
    await nextTick()

    expect(document.body.querySelector<HTMLElement>('.modal-body')?.scrollTop).toBe(0)
    wrapper.unmount()
  })

  it('uses a unique accessible title for each open dialog', async () => {
    const DialogHost = defineComponent({
      components: { BaseDialog },
      template: `
        <BaseDialog show title="Account details">details</BaseDialog>
        <BaseDialog show title="Delete account">confirmation</BaseDialog>
      `
    })
    const wrapper = mount(DialogHost, {
      attachTo: document.body,
      global: { stubs: { Icon: true } }
    })

    await nextTick()
    const dialogs = Array.from(document.body.querySelectorAll<HTMLElement>('[role="dialog"]'))
    expect(dialogs).toHaveLength(2)

    const titleIDs = dialogs.map((dialog) => dialog.getAttribute('aria-labelledby'))
    expect(new Set(titleIDs).size).toBe(2)
    expect(titleIDs.map((id) => document.getElementById(id!)?.textContent?.trim())).toEqual([
      'Account details',
      'Delete account'
    ])

    wrapper.unmount()
  })
})
