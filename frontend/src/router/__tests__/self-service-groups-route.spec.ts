import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

const routerPath = resolve(dirname(fileURLToPath(import.meta.url)), '../index.ts')
const routerSource = readFileSync(routerPath, 'utf8')

describe('self-service groups route', () => {
  it('registers the user page behind authentication and the self-service gate', () => {
    expect(routerSource).toContain("path: '/groups'")
    expect(routerSource).toContain("name: 'SelfServiceGroups'")
    expect(routerSource).toContain("import('@/views/user/SelfServiceGroupsView.vue')")

    const routeBlock = routerSource.match(/\{\s*path: '\/groups',[\s\S]*?\n {2}\},/)
    expect(routeBlock).not.toBeNull()
    expect(routeBlock?.[0]).toContain('requiresAuth: true')
    expect(routeBlock?.[0]).toContain('requiresSelfServiceHosting: true')
    expect(routeBlock?.[0]).toContain("titleKey: 'selfServiceGroups.title'")
  })

  it('keeps the page restricted in simple mode', () => {
    const restrictedBlock = routerSource.match(/const restrictedPaths = \[[\s\S]*?\n {4}\]/)
    expect(restrictedBlock).not.toBeNull()
    expect(restrictedBlock?.[0]).toContain("'/groups'")
  })
})
