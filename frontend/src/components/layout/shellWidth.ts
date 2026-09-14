/** How wide a screen's content column is. The top bar takes the same value so
 *  the bar and the page under it share one edge. */
export type ShellWidth = 'narrow' | 'default' | 'wide'

export function shellWidthClass(width: ShellWidth = 'default'): string {
  switch (width) {
    case 'narrow':
      return 'page-shell--narrow'
    case 'wide':
      return 'page-shell--wide'
    default:
      return ''
  }
}
