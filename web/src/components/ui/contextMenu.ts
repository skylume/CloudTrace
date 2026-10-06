/** 右键菜单的一项。 */
export interface ContextMenuItem {
  label: string
  action: () => void
  disabled?: boolean
}
