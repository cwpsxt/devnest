// Stroke icons on a 24x24 grid.
export const ICONS = {
  home: 'M3 11l9-8 9 8M5 10v10h5v-6h4v6h5V10',
  box: 'M21 8l-9-5-9 5v8l9 5 9-5V8zM3 8l9 5 9-5M12 13v8',
  server: 'M3 4h18v7H3zM3 13h18v7H3zM7 7.5h.01M7 16.5h.01',
  layers: 'M12 3l9 5-9 5-9-5 9-5zM3 13l9 5 9-5M3 17l9 5 9-5',
  zap: 'M13 2L4 14h7l-1 8 9-12h-7l1-8z',
  trash: 'M4 7h16M9 7V4h6v3M6 7l1 13h10l1-13M10 11v6M14 11v6',
  list: 'M8 6h13M8 12h13M8 18h13M3.5 6h.01M3.5 12h.01M3.5 18h.01',
  settings:
    'M12 15a3 3 0 100-6 3 3 0 000 6zM19.4 15a1.6 1.6 0 00.3 1.8l.1.1a2 2 0 11-2.8 2.8l-.1-.1a1.6 1.6 0 00-2.7 1.1V21a2 2 0 11-4 0v-.1a1.6 1.6 0 00-2.7-1.1l-.1.1a2 2 0 11-2.8-2.8l.1-.1a1.6 1.6 0 00-1.1-2.7H3a2 2 0 110-4h.1a1.6 1.6 0 001.1-2.7l-.1-.1a2 2 0 112.8-2.8l.1.1a1.6 1.6 0 002.7-1.1V3a2 2 0 114 0v.1a1.6 1.6 0 002.7 1.1l.1-.1a2 2 0 112.8 2.8l-.1.1a1.6 1.6 0 001.1 2.7H21a2 2 0 110 4h-.1a1.6 1.6 0 00-1.5 1.3z',
  search: 'M11 18a7 7 0 100-14 7 7 0 000 14zM21 21l-5-5',
  folder: 'M3 6h6l2 2h10v11H3z',
  copy: 'M9 9h11v11H9zM5 15H4V4h11v1',
  play: 'M6 4l14 8-14 8V4z',
  stop: 'M6 6h12v12H6z',
  refresh: 'M20 11a8 8 0 10-2.3 6.3M20 5v6h-6',
  external: 'M14 4h6v6M20 4l-9 9M18 14v6H4V6h6',
  check: 'M5 12l5 5L20 7',
  x: 'M6 6l12 12M18 6L6 18',
  download: 'M12 4v12M7 11l5 5 5-5M4 20h16',
  file: 'M14 3H6v18h12V7l-4-4zM14 3v4h4M9 13h6M9 17h6',
  swap: 'M7 4L3 8l4 4M3 8h14M17 20l4-4-4-4M21 16H7',
  alert: 'M12 3l10 18H2L12 3zM12 10v5M12 18h.01',
  info: 'M12 21a9 9 0 100-18 9 9 0 000 18zM12 11v6M12 7.5h.01',
} as const

export type IconName = keyof typeof ICONS
