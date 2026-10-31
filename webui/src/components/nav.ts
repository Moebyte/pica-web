export interface NavItem {
  to: string
  title: string
  /** inner SVG markup (uses currentColor) */
  icon: string
  match: string[]
}

const strokeAttrs = `fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"`

/** Single source of nav items for sidebar + bottom tabs. */
export const NAV_ITEMS: NavItem[] = [
  {
    to: '/',
    title: '探索',
    icon: `<path ${strokeAttrs} d="M4.5 5.5a1 1 0 0 1 1-1h4v7h-5zM4.5 19.5a1 1 0 0 0 1 1h4v-7h-5zM14.5 20.5h4a1 1 0 0 0 1-1v-5h-5zM14.5 4.5v7h5v-5a1 1 0 0 0-1-1z"/>`,
    match: ['/'],
  },
  {
    to: '/search',
    title: '搜索',
    icon: `<path ${strokeAttrs} d="M10.5 4a6.5 6.5 0 1 1 0 13 6.5 6.5 0 0 1 0-13zM20 20l-4.9-4.9"/>`,
    match: ['/search'],
  },
  {
    to: '/favorites',
    title: '收藏',
    icon: `<path ${strokeAttrs} d="M12 20s-7.5-4.6-9.3-8.9A5 5 0 0 1 12 6.3 5 5 0 0 1 21.3 11C19.5 15.4 12 20 12 20z"/>`,
    match: ['/favorites'],
  },
  {
    to: '/history',
    title: '历史',
    icon: `<path ${strokeAttrs} d="M12 7.5V4l-3.5 3.5zM12 4.8A7.8 7.8 0 1 1 4.2 12M12 8.2v4.3l3 2"/>`,
    match: ['/history'],
  },
  {
    to: '/downloads',
    title: '下载',
    icon: `<path ${strokeAttrs} d="M12 4v10m0 0-4-4m4 4 4-4M5 19.5h14"/>`,
    match: ['/downloads'],
  },
  {
    to: '/settings',
    title: '设置',
    icon: `<path ${strokeAttrs} d="M4 7h9M17 7h3M4 17h3M11 17h9"/><circle ${strokeAttrs} cx="15" cy="7" r="2.2"/><circle ${strokeAttrs} cx="8" cy="17" r="2.2"/>`,
    match: ['/settings'],
  },
]
