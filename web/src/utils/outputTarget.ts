/**
 * 输出目标的接缝。
 *
 * 桌面浏览器只有「下载」一种去处；Android 壳里同一份文件应该走系统分享。
 * 页面与 store 只说「把这份东西交给用户」，具体怎么交由此处决定，前端任何
 * 地方都不需要知道本地路径。
 */
import { api } from '@/api/rest'

export interface OutputFile {
  name: string
  /** 后端生成好的下载地址；与 blob 二选一。 */
  url?: string
  /** 前端自己拼出来的内容，例如旧档位导出的 JSON。 */
  blob?: Blob
}

export interface OutputTarget {
  readonly id: 'download' | 'share'
  save(file: OutputFile): void
}

/** 把 blob 变成一次浏览器下载。 */
function downloadBlob(file: OutputFile): void {
  if (!file.blob) return
  const url = URL.createObjectURL(file.blob)
  const link = document.createElement('a')
  link.href = url
  link.download = file.name
  link.rel = 'noopener'
  document.body.appendChild(link)
  link.click()
  link.remove()
  URL.revokeObjectURL(url)
}

const downloadTarget: OutputTarget = {
  id: 'download',
  save(file) {
    if (file.url) api.download(file.url, file.name)
    else downloadBlob(file)
  },
}

const shareTarget: OutputTarget = {
  id: 'share',
  save(file) {
    if (!file.blob) {
      downloadTarget.save(file)
      return
    }
    const data = new File([file.blob], file.name, { type: file.blob.type })
    void navigator.share({ files: [data] }).catch(() => downloadTarget.save(file))
  },
}

/** 只有带 blob 的文件才能分享；服务端地址没有本地内容可交出去。 */
export function canShareFile(file: OutputFile): boolean {
  if (!file.blob || typeof navigator === 'undefined' || typeof navigator.canShare !== 'function') return false
  try {
    return navigator.canShare({ files: [new File([file.blob], file.name, { type: file.blob.type })] })
  } catch {
    return false
  }
}

export function pickOutputTarget(file: OutputFile): OutputTarget {
  return canShareFile(file) ? shareTarget : downloadTarget
}
