/**
 * Deterministic mock dataset for the semantic /api/src/* endpoints.
 * Used by the dev server (and vite preview) only while the real gateway
 * semantic API is not deployed yet. Everything is seeded so E2E runs are
 * reproducible.
 */

export interface MockChapter {
  id: string
  title: string
  order: number
}

export interface MockComic {
  id: string
  title: string
  subtitle: string
  author: string
  cover: string
  pageCount: number
  chapters: MockChapter[]
  tags: string[]
  categories: string[]
  languages: string[]
  favorites: number
  uploadDate: number
}

export function mulberry32(seed: number): () => number {
  let a = seed >>> 0
  return () => {
    a |= 0
    a = (a + 0x6d2b79f5) | 0
    let t = Math.imul(a ^ (a >>> 15), 1 | a)
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296
  }
}

export function hashString(s: string): number {
  let h = 2166136261
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i)
    h = Math.imul(h, 16777619)
  }
  return h >>> 0
}

const JM_TITLES = [
  '弟の身代わりになった姉',
  '隣人に寝取られた妻',
  '夏休みの補習授業',
  '人妻の誘惑',
  '清楚なクラスメイトの秘密',
  '母の友達とまさかの同居',
  'OLの残業は今日も長い',
  '温泉旅館の若女将',
  '保健室の放課後',
  '巨乳上司の出張同行',
  '僕の姉は有名配信者',
  'ラブホで待ち合わせ',
  '義母との秘密の家事分担',
  '合宿所の夜は眠れない',
  '風紀委員会の校外指導',
  '看護師の夜勤帯',
]

const PICACG_TITLES = [
  '白山華凜のシドウ制度',
  '姉弟の距離感',
  '放課後ろいある',
  '無言の誘惑',
  '先輩と僕の研究室',
  '幼馴染と夏の思い出',
  '嘘つきな妹',
  '転校生はクールな妹系',
  'わたしの先生',
  '君との距離、ゼロメートル',
  'puberty blue',
  'ひみつの放課後',
  '百合の花咲く庭',
  '醉後の一夜',
  '宿題おしえてください',
  '雨曜日の傘',
]

const JM_AUTHORS = ['diletta', '井藤ななみ', '北野タダシ', 'COMIC 天魔', 'やまだ', '黒柘榴', 'みこ_soft', 'hamao']
const PICACG_AUTHORS = [' 作者A', 'keke', '吟游诗人', '纯情小处男', 'aska', '柚子猫', '黑暗料理界', '']
const JM_CATEGORIES = ['漢化', '日本語', '短篇', '單行本', '裏番', '禁漫精神病院', '純愛', '校園']
const PICACG_CATEGORIES = ['恋爱', '校园', '奇幻', '日常', '搞笑', '治愈', '都市', '悬疑']
const LANGUAGES = ['中文', '日本語', 'English']

function pick<T>(arr: T[], i: number): T {
  return arr[i % arr.length] as T
}

function chapterCount(rand: () => number, src: string): number {
  if (src === 'jm') return 1
  const r = rand()
  if (r < 0.35) return 1
  if (r < 0.7) return 3
  if (r < 0.9) return 5
  return 8
}

export function buildPool(src: string): MockComic[] {
  const size = src === 'jm' ? 140 : 120
  const rand = mulberry32(hashString(`pool:${src}`))
  const titles = src === 'jm' ? JM_TITLES : PICACG_TITLES
  const authors = src === 'jm' ? JM_AUTHORS : PICACG_AUTHORS
  const cats = src === 'jm' ? JM_CATEGORIES : PICACG_CATEGORIES
  const pool: MockComic[] = []
  for (let i = 0; i < size; i++) {
    const id =
      src === 'jm'
        ? String(227000 + i * 13)
        : hexId(hashString(`${src}:${i}`)).toString()
    const dup = Math.floor(i / titles.length) + 1
    const title = `${pick(titles, i)}${dup > 1 ? ` (${dup})` : ''}`
    const author = pick(authors, i * 3 + 1).trim()
    const pageCount = src === 'jm' ? 8 + Math.floor(rand() * 32) : 12 + Math.floor(rand() * 60)
    const chCount = chapterCount(rand, src)
    const chapters: MockChapter[] = Array.from({ length: chCount }, (_, n) => ({
      id: `${id}-c${n + 1}`,
      title: chCount === 1 ? '全一話' : `第 ${n + 1} 話`,
      order: n + 1,
    }))
    const tagCount = 2 + Math.floor(rand() * 3)
    const tags: string[] = []
    for (let t = 0; t < tagCount; t++) {
      const tag = pick(cats, i * 5 + t * 3)
      if (!tags.includes(tag)) tags.push(tag)
    }
    const catCount = 1 + Math.floor(rand() * 2)
    const categories: string[] = []
    for (let t = 0; t < catCount; t++) {
      const c = pick(cats, i + t * 7)
      if (!categories.includes(c)) categories.push(c)
    }
    pool.push({
      id,
      title,
      subtitle: author ? `${author} (${src === 'jm' ? '作者' : '汉化组'})` : '未知作者',
      author: author || '未知',
      cover: `/api/img/mock/cover-${src}-${id}.svg`,
      pageCount,
      chapters,
      tags,
      categories,
      languages: [pick(LANGUAGES, i)],
      favorites: 500 + Math.floor(rand() * 20000),
      uploadDate: Math.floor(Date.now() / 1000) - i * 86400 * 3,
    })
  }
  return pool
}

function hexId(seed: number): string {
  const rand = mulberry32(seed)
  let s = ''
  for (let i = 0; i < 24; i++) s += Math.floor(rand() * 16).toString(16)
  return s
}

export const POOLS: Record<string, MockComic[]> = {
  jm: buildPool('jm'),
  picacg: buildPool('picacg'),
}

export const TABS: Record<string, { id: string; title: string }[]> = {
  jm: [
    { id: 'mr', title: '最新' },
    { id: 'mv', title: '最多观看' },
    { id: 'mp', title: '最多收藏' },
    { id: 'mfm', title: '最多喜欢' },
    { id: 'long', title: '長篇' },
    { id: 'short', title: '短篇' },
  ],
  picacg: [
    { id: 'collect', title: '大家都在看' },
    { id: 'new', title: '最新上架' },
    { id: 'day', title: '日榜' },
    { id: 'week', title: '周榜' },
    { id: 'month', title: '月榜' },
    { id: 'random', title: '随机本子' },
  ],
}

/** Deterministic per-tab ordering. */
export function orderedPool(src: string, tab: string): MockComic[] {
  const pool = POOLS[src] ?? []
  const rand = mulberry32(hashString(`${src}:${tab}`))
  const arr = [...pool]
  for (let i = arr.length - 1; i > 0; i--) {
    const j = Math.floor(rand() * (i + 1))
    const tmp = arr[i] as MockComic
    arr[i] = arr[j] as MockComic
    arr[j] = tmp
  }
  if (tab === 'long') arr.sort((a, b) => b.pageCount - a.pageCount)
  if (tab === 'short') arr.sort((a, b) => a.pageCount - b.pageCount)
  return arr
}

export function findComic(src: string, id: string): MockComic | undefined {
  return (POOLS[src] ?? []).find((c) => c.id === id)
}
