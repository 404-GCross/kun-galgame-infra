import type { KunUISize } from '../ui/type'

export interface KunAvatarProps {
  user: KunUser
  size?: KunUISize | 'original' | 'original-sm'
  isNavigation?: boolean
  className?: string
  imageClassName?: string
  disableFloating?: boolean
  floatingPosition?: 'top' | 'bottom' | 'left' | 'right'
}
