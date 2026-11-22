/**
 * 历史列表的评分口径标注。
 *
 * 只钉一件事：索引里的 score_version 与后端下发的当前版本对不上时，这一条要
 * 被标成「旧口径」。公式改过之后旧记录的分数与新记录不是同一把尺子量出来
 * 的，混在一张表里静默排序会误导。
 */
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it } from 'vitest'

import type { HistoryIndexEntry } from '@/api/types'
import { useHistoryStore } from '@/stores/history'

import HistoryList from './HistoryList.vue'

function entry(patch: Partial<HistoryIndexEntry> = {}): HistoryIndexEntry {
  return {
    id: 'x',
    type: 'scan',
    ip_version: 4,
    created_at: new Date().toISOString(),
    count: 3,
    starred: false,
    ...patch,
  }
}

describe('HistoryList 评分口径', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('版本落后的标旧口径，同版本与没有标记的不标', () => {
    useHistoryStore().currentScoreVersion = 3
    const wrapper = mount(HistoryList, {
      props: {
        entries: [
          entry({ id: 'old', score_version: 2 }),
          entry({ id: 'cur', score_version: 3 }),
          entry({ id: 'plain' }),
        ],
        compareIds: [],
      },
    })

    const marks = wrapper.findAll('.chip.warn')
    expect(marks).toHaveLength(1)
    expect(marks[0]?.text()).toBe('旧口径')
  })

  it('中止的批次单独标出来', () => {
    useHistoryStore().currentScoreVersion = 3
    const wrapper = mount(HistoryList, {
      props: {
        entries: [entry({ id: 'cut', aborted: true }), entry({ id: 'full' })],
        compareIds: [],
      },
    })

    const marks = wrapper.findAll('.chip.warn')
    expect(marks).toHaveLength(1)
    expect(marks[0]?.text()).toBe('已中止')
  })

  it('后端还没给当前版本时不猜，一条都不标', () => {
    useHistoryStore().currentScoreVersion = 0
    const wrapper = mount(HistoryList, {
      props: { entries: [entry({ score_version: 2 })], compareIds: [] },
    })

    expect(wrapper.find('.chip.warn').exists()).toBe(false)
  })
})
