/**
 * 设置项的结构表。
 *
 * 只描述**结构**（分组、键路径、控件类型、取值范围），文案另走 `settingsText`
 * 的中英两套映射——混在一起会让「改一句提示」变成改一个数据结构。
 *
 * 键路径用点号：`scan.workers`。与配置文件的嵌套结构、以及后端
 * `settings/reset` 接受的键一致，中间不做任何转换。
 */

export type FieldKind = 'bool' | 'int' | 'float' | 'text' | 'enum' | 'list' | 'path'

export interface SettingField {
  /** 点号路径，如 `scan.workers`。 */
  path: string
  kind: FieldKind
  /** 数值范围（kind 为 int / float 时有效）。 */
  min?: number
  max?: number
  /**
   * 上限跟随另一个配置项（点号路径）。
   *
   * 有的项本身没有固定上限，它的上限由用户自己定的另一项决定（如并发跟着全局
   * 并发上限走）。不跟的话界面会允许填一个必定被拒的值，看起来像程序的错。
   */
  maxPath?: string
  /** 小数位（kind 为 float 时有效）。 */
  step?: number
  /** 枚举取值（kind 为 enum 时有效）。 */
  options?: string[]
  /** 该组里排在前面、值得优先展示的项。 */
  primary?: boolean
  /**
   * 配置项在、但还没有任何代码读它。
   *
   * 标出来并禁用，而不是照常显示：一个改了什么都不会发生的开关，比没有这个
   * 开关更伤信任——用户会以为是自己没配对。等对应功能落地时去掉这个标记。
   */
  pending?: boolean
}

export interface SettingGroup {
  id: string
  fields: SettingField[]
}

/**
 * 分组与字段。
 *
 * 分组顺序按「改动的频率 × 影响面」排：界面与扫描在最前（人人都会碰），
 * 服务面板与高级在最后（配一次就不再进）。设置页可以密，但常用的必须在
 * 触手可及的地方。
 */
export const SETTING_GROUPS: SettingGroup[] = [
  {
    id: 'ui',
    fields: [
      { path: 'ui.theme', kind: 'enum', options: ['system', 'dark', 'light'], primary: true },
      { path: 'ui.lang', kind: 'enum', options: ['zh', 'en'] },
      { path: 'ui.density', kind: 'enum', options: ['auto', 'simple', 'advanced'] },
      { path: 'ui.start_page', kind: 'enum', options: ['scan', 'result', 'history', 'settings'] },
      { path: 'ui.time_format', kind: 'enum', options: ['local', 'utc'] },
      { path: 'ui.font_scale', kind: 'enum', options: ['small', 'medium', 'large'], primary: true },
      { path: 'ui.table_density', kind: 'enum', options: ['compact', 'normal', 'comfortable'] },
      // 结果表目前一次渲染全部结果，还没有翻页。
      { path: 'ui.page_size', kind: 'int', min: 20, max: 500 },
      { path: 'ui.animation', kind: 'bool', primary: true },
      { path: 'ui.contrast', kind: 'bool', primary: true },
      { path: 'ui.remember_state', kind: 'bool' },
      { path: 'ui.adaptive_enabled', kind: 'bool' },
      { path: 'ui.close_to_tray', kind: 'bool' },
      { path: 'ui.adaptive_allow_preset', kind: 'bool' },
    ],
  },
  {
    id: 'scan',
    fields: [
      // 上限跟着全局并发上限走：那个值用户可以自己调，写死一个数会让调它没意义。
      { path: 'scan.workers', kind: 'int', min: 1, max: 2000, maxPath: 'net.max_workers', primary: true },
      { path: 'scan.sample_max', kind: 'int', min: 0, max: 5000, primary: true },
      { path: 'scan.latency_threshold', kind: 'int', min: 1, max: 5000, primary: true },
      { path: 'scan.ping_times', kind: 'int', min: 0, max: 20 },
      { path: 'scan.port', kind: 'int', min: 1, max: 65535 },
      { path: 'scan.timeout_ms', kind: 'int', min: 100, max: 30000 },
      { path: 'scan.retry', kind: 'int', min: 0, max: 10 },
      { path: 'scan.mode', kind: 'enum', options: ['tcping', 'httping'] },
      { path: 'scan.two_phase', kind: 'bool' },
      { path: 'scan.verify_nodes', kind: 'bool' },
      { path: 'scan.usability_check', kind: 'bool' },
      { path: 'scan.source_mode', kind: 'enum', options: ['official', 'custom', 'both'] },
      { path: 'scan.allowed_regions', kind: 'list' },
      { path: 'scan.blocked_regions', kind: 'list' },
      { path: 'scan.pre_filter_ports', kind: 'list' },
    ],
  },
  {
    id: 'speed',
    fields: [
      // 上限 16 是配置校验的硬边界，不是随手取的数：并发越高，多个目标同时
      // 下载就越会互相抢带宽，测出来的是平均值而不是任何一个节点的真实速度。
      { path: 'speed.concurrency', kind: 'int', min: 1, max: 16, primary: true },
      { path: 'speed.target_qualified', kind: 'int', min: 1, max: 100, primary: true },
      { path: 'speed.min_speed', kind: 'int', min: 0, max: 1000 },
      { path: 'speed.url_mode', kind: 'enum', options: ['auto', 'official', 'mobile_friendly', 'mobile_only', 'custom'] },
      { path: 'speed.custom_url', kind: 'text' },
      { path: 'speed.download_duration_s', kind: 'int', min: 1, max: 60 },
      // 至少熔断一次：0 会让限速一路测下去，拿到的速度全是假的。
      { path: 'speed.breaker_429', kind: 'int', min: 1, max: 100 },
      // 下限从 1 起：0 在请求里与「没带这个字段」无法区分，后端会把它当成
      // 未设置并补成 1.2 秒。与其让 0 静默变成别的数，不如不给这个选项——
      // 想要「几乎不等」填 1 即可，那个值会被原样采纳。
      { path: 'speed.interval_ms', kind: 'int', min: 1, max: 5000 },
      { path: 'speed.max_download_mb', kind: 'int', min: 0, max: 1000 , pending: true },
      { path: 'speed.per_region_topn', kind: 'int', min: 0, max: 100 },
      { path: 'speed.weight_speed', kind: 'float', min: 0, max: 10, step: 0.1 },
      { path: 'speed.weight_latency', kind: 'float', min: 0, max: 10, step: 0.1 },
      { path: 'speed.weight_jitter', kind: 'float', min: 0, max: 10, step: 0.1 },
    ],
  },
  {
    id: 'source',
    fields: [
      { path: 'source.merge_strategy', kind: 'enum', options: ['union', 'intersect'] , pending: true },
      { path: 'source.timeout_ms', kind: 'int', min: 100, max: 60000, primary: true },
      { path: 'source.retry', kind: 'int', min: 0, max: 10, primary: true },
      { path: 'source.retry_interval_ms', kind: 'int', min: 0, max: 10000 , pending: true },
    ],
  },
  {
    id: 'net',
    fields: [
      { path: 'net.connect_timeout_ms', kind: 'int', min: 100, max: 60000, primary: true, pending: true },
      { path: 'net.use_tls', kind: 'enum', options: ['auto', 'true', 'false'], primary: true },
      { path: 'net.ip_version', kind: 'enum', options: ['auto', 'v4', 'v6'] },
      { path: 'net.max_workers', kind: 'int', min: 1, max: 10000, primary: true },
      { path: 'net.proxy', kind: 'text' },
      { path: 'net.force_direct', kind: 'bool' },
      { path: 'net.custom_dns', kind: 'list' , pending: true },
      { path: 'net.dns_fallback', kind: 'bool' , pending: true },
      { path: 'net.user_agent', kind: 'text' },
    ],
  },
  {
    id: 'geo',
    fields: [
      { path: 'geo.asn_source', kind: 'enum', options: ['iptoasn', 'geolite2_mmdb', 'off'], primary: true },
      { path: 'geo.asn_db_path', kind: 'path' },
      { path: 'geo.asn_auto_update', kind: 'bool', primary: true },
      { path: 'geo.asn_update_interval_days', kind: 'int', min: 0, max: 365, primary: true },
      { path: 'geo.geo_warn_enabled', kind: 'bool' },
      { path: 'geo.filter_asn', kind: 'list' },
    ],
  },
  {
    id: 'history',
    fields: [
      { path: 'history.keep_count', kind: 'int', min: 1, max: 500, primary: true },
      { path: 'history.keep_mode', kind: 'enum', options: ['count', 'days'] },
      { path: 'history.keep_days', kind: 'int', min: 1, max: 3650 },
      { path: 'history.auto_save', kind: 'bool' },
      { path: 'history.auto_dedup', kind: 'bool' },
      { path: 'data.dir', kind: 'path' },
      { path: 'data.portable', kind: 'bool' },
    ],
  },
  {
    id: 'export',
    fields: [
      { path: 'export.default_format', kind: 'enum', options: ['csv', 'json', 'txt'], primary: true },
      { path: 'export.csv_bom', kind: 'bool' },
      { path: 'export.default_fields', kind: 'enum', options: ['all', 'slim', 'ip_port'] },
      { path: 'export.filename_template', kind: 'text' },
    ],
  },
  {
    id: 'server',
    fields: [
      { path: 'server.port', kind: 'int', min: 1, max: 65535, primary: true },
      { path: 'server.bind', kind: 'enum', options: ['127.0.0.1', '0.0.0.0'], primary: true },
      { path: 'server.token', kind: 'text' },
      { path: 'server.session_ttl_min', kind: 'int', min: 1, max: 10080 },
      { path: 'server.open_browser', kind: 'bool', primary: true },
      { path: 'server.autostart', kind: 'bool', pending: true },
    ],
  },
  {
    id: 'notify',
    fields: [
      { path: 'notify.on_done', kind: 'bool', primary: true, pending: true },
      { path: 'notify.on_fail', kind: 'bool', primary: true, pending: true },
      { path: 'notify.web', kind: 'bool', pending: true },
      { path: 'notify.tray', kind: 'bool', pending: true },
      { path: 'notify.sound', kind: 'bool', pending: true },
    ],
  },
  {
    id: 'advanced',
    fields: [
      { path: 'advanced.log_level', kind: 'enum', options: ['debug', 'info', 'warn', 'error'], primary: true },
      // 日志目前只写控制台，没有落盘的文件可清理。
      { path: 'advanced.log_keep_days', kind: 'int', min: 1, max: 365, pending: true },
      { path: 'advanced.check_update', kind: 'bool', pending: true },
    ],
  },
]

/** 文案：路径 → 标签与说明。 */
interface FieldText {
  label: string
  hint: string
}

export const settingsText: Record<'zh' | 'en', Record<string, FieldText>> = {
  zh: {
    'ui.theme': { label: '主题', hint: '深色、浅色或跟随系统。切换即时生效，不需要刷新' },
    'ui.lang': { label: '界面语言', hint: '中英文切换' },
    'ui.font_scale': { label: '字号', hint: '小 / 中 / 大三档，整体缩放而不破坏布局' },
    'ui.table_density': { label: '表格密度', hint: '只改行高，不动字号——两者互不干扰' },
    'ui.page_size': { label: '每页条数', hint: '结果表一页显示多少行。条数超过一页时表格下方会出现翻页控件' },
    'ui.animation': { label: '动效', hint: '关掉后所有过渡与动画变为瞬时。系统设置了减少动效时同样会关' },
    'ui.contrast': { label: '高对比度', hint: '把文字推到极值、边框加实、去掉投影。与深浅主题独立，可叠加使用' },
    'ui.remember_state': {
      label: '记住界面状态',
      hint: '下次打开回到上次停留的页面，并记住参数面板是否展开。关掉之后每次都从启动页面开始；参数值本身存在配置里，与这一项无关',
    },
    'ui.adaptive_enabled': { label: '智能自适应', hint: '按网络环境自动调整参数。只填空白，绝不覆盖你改过的值' },
    'ui.close_to_tray': { label: '关闭窗口时收进托盘', hint: '关掉窗口后继续在后台跑；真要退出请用托盘菜单里的「退出」。只对桌面版有效' },
    'ui.adaptive_allow_preset': { label: '自适应可改档位值', hint: '关掉后自适应只提示、不修改档位填进去的参数' },

    'scan.workers': {
      label: '并发',
      hint: '同时测多少个地址。弱网或老路由建议 50–100，超过 300 会明显加重路由器负担。上限由「并发上限」那一项决定',
    },
    'scan.sample_max': { label: '采样上限', hint: '最多挑多少个地址来测。500 够用，5000 更全面但更慢' },
    'scan.latency_threshold': { label: '延迟阈值', hint: '超过这个延迟的节点直接淘汰' },
    'scan.ping_times': { label: '探测次数', hint: '每个地址测几次。0 表示自动' },
    'scan.port': { label: '默认端口', hint: '默认 443。部分网络下 2053 / 2083 更稳' },
    'scan.timeout_ms': { label: '探测超时', hint: '单次探测等多久算失败' },
    'scan.retry': { label: '失败重试', hint: '探测失败后重试几次' },
    'scan.mode': { label: '探测模式', hint: 'TCPing 更快，HTTPing 更接近真实访问但阈值会放宽' },
    'scan.two_phase': { label: '两阶段扫描', hint: '先低采样粗筛，再对达标节点精测。开着更快' },
    'scan.verify_nodes': { label: '采集节点明细', hint: '关掉会少发大量请求、明显更快，代价是结果里没有地区与运营商' },
    'scan.usability_check': { label: '测速前可用性校验', hint: '测速之前再确认一次节点还活着' },

    'speed.concurrency': { label: '测速并发', hint: '同时下载多少个，上限 16。太高容易触发限速，测出来的速度也不准' },
    'speed.target_qualified': { label: '合格节点数', hint: '测到多少个达标节点就停' },
    'speed.min_speed': { label: '最低速度', hint: '低于这个速度算不合格' },
    'speed.url_mode': { label: '测速源', hint: '自动会根据你的出口运营商选；也可固定用官方源或指定地址' },
    'speed.custom_url': { label: '自定义测速地址', hint: '仅在测速源选「自定义」时使用' },
    'speed.download_duration_s': { label: '单次测速时长', hint: '每个节点下载多久' },
    'speed.breaker_429': { label: '限速熔断阈值', hint: '连续多少次被限速就停止。设成 1 等于一遇到限速就停' },

    'geo.asn_source': { label: 'ASN 数据源', hint: 'iptoasn 免账号体积小；GeoLite2 查询更快但文件大。关掉则不显示运营商' },
    'geo.asn_db_path': { label: '库文件位置', hint: '可指向目录或文件。手动放置的文件会被优先使用' },
    'geo.asn_auto_update': { label: '自动更新 ASN 库', hint: '关掉后不发任何请求，只用本地已有的' },
    'geo.asn_update_interval_days': { label: '更新周期（天）', hint: '超过这个天数就后台更新。0 表示仅手动' },
    'geo.geo_warn_enabled': { label: '代理出口提示', hint: '检测到本机出口不是国内时给一条横幅提示。仅提示，不阻断任务' },
    'geo.filter_asn': { label: '运营商过滤', hint: '只看某些运营商的节点。留空表示不过滤' },

    'history.keep_count': { label: '保留份数', hint: '最多保留多少份历史。收藏的记录不占名额' },
    'history.keep_mode': { label: '保留方式', hint: '按份数或按天数清理' },
    'history.keep_days': { label: '保留天数', hint: '保留方式选「按天数」时生效' },
    'history.auto_save': { label: '自动存档', hint: '任务跑完自动存一份历史' },

    'export.default_format': { label: '默认导出格式', hint: 'CSV 带 BOM，Excel 打开不乱码' },

    'server.port': { label: '面板端口', hint: '改完需要重启才生效。旧版本用过 18543，当前统一为 17443' },
    'server.bind': { label: '监听地址', hint: '仅本机时只有这台电脑能访问；局域网会让同网段的设备都能打开，此时务必设好访问 Token' },
    'server.token': { label: '访问 Token', hint: '开放局域网访问时的口令。留空表示不校验，仅本机访问时可以这样' },
    'server.session_ttl_min': { label: '会话有效期（分钟）', hint: '登录后多久需要重新输入 Token' },
    'server.open_browser': { label: '启动后打开浏览器', hint: '双击运行时省去手动输地址。开发时可用 --no-browser 跳过' },
    'server.autostart': { label: '开机自启', hint: '仅桌面版有效' },

    'notify.on_done': { label: '任务完成提醒', hint: '扫描或测速结束时提示一次' },
    'notify.on_fail': { label: '任务失败提醒', hint: '任务出错时提示' },
    'notify.web': { label: '浏览器通知', hint: '需要浏览器授权；被拒绝时不会重复询问' },
    'notify.tray': { label: '托盘通知', hint: '仅桌面版有效' },
    'notify.sound': { label: '提示音', hint: '默认关闭，避免在公共场合突然出声' },

    'advanced.log_level': { label: '日志级别', hint: '排查问题时调到 debug。改完需要重启才生效' },
    'ui.density': { label: '参数密度', hint: '简单模式下参数默认折叠；一旦展开过高级模式就会永久记住' },
    'ui.start_page': {
      label: '启动页面',
      hint: '下次打开直接落在哪一页。关掉「记住界面状态」时每次都从这里开始',
    },
    'ui.time_format': { label: '时间显示', hint: '本地时间或 UTC' },

    'scan.source_mode': { label: '来源模式', hint: '官方网段、自定义来源，或两者合并。扫描页会自动设置它' },
    'scan.allowed_regions': { label: '地区白名单', hint: '只保留这些地区的节点，留空表示不限。地区未知的会保守保留' },
    'scan.blocked_regions': { label: '地区黑名单', hint: '排除这些地区的节点' },
    'scan.pre_filter_ports': { label: '前置端口过滤', hint: '在 TCP 测试之前就排除这些端口的候选，省下探测时间' },

    'speed.interval_ms': { label: '测速间隔', hint: '每个目标之间等多久再测下一个，用来降低被限速的概率' },
    'speed.max_download_mb': { label: '单次最大下载量', hint: '超过就停止该节点的测速。0 表示不限' },
    'speed.per_region_topn': { label: '每地区取前 N 名', hint: '按地区各取前几名参与测速。0 表示不按地区分配' },
    'speed.weight_speed': { label: '评分权重 · 速度', hint: '综合评分里速度占的比重' },
    'speed.weight_latency': { label: '评分权重 · 延迟', hint: '综合评分里延迟占的比重' },
    'speed.weight_jitter': { label: '评分权重 · 抖动', hint: '综合评分里抖动占的比重。抖动大意味着忽快忽慢' },

    'source.merge_strategy': { label: '多源合并方式', hint: '并集取所有来源的地址；交集只取同时出现在所有来源里的' },
    'source.timeout_ms': { label: '拉取超时', hint: '单个远端源等多久算失败' },
    'source.retry': { label: '拉取重试', hint: '远端源失败后重试几次' },
    'source.retry_interval_ms': { label: '重试间隔', hint: '两次重试之间等多久' },

    'net.connect_timeout_ms': { label: '连接超时', hint: '建立 TCP 连接等多久算失败' },
    'net.use_tls': { label: 'TLS 探测', hint: '自动时按端口推断：443 与 8443 走 TLS，其余不走' },
    'net.ip_version': { label: '地址族', hint: '只测 IPv4、只测 IPv6，或两者都测' },
    'net.max_workers': {
      label: '并发上限',
      hint: '面板允许设到多大并发。默认 2000；调低它等于给自己加一道护栏，调高则放开。这是你自己定的上限，不是系统的限制',
    },
    'net.proxy': { label: 'HTTP 代理', hint: '拉取官方网段与远端源时走这个代理。留空表示直连' },
    'net.force_direct': { label: '强制直连', hint: '忽略环境变量里的代理设置。节点探测始终直连，不受这里影响' },
    'net.custom_dns': { label: '自定义 DNS', hint: '解析域名时用这些 DNS 服务器，留空用系统默认' },
    'net.dns_fallback': { label: 'DNS 回退', hint: '系统 DNS 解析失败时回退到内置的公共 DNS' },
    'net.user_agent': { label: '请求 User-Agent', hint: '拉取数据源时用的 UA。默认浏览器 UA，被拦时可能有用' },

    'history.auto_dedup': { label: '自动去重', hint: '同参数短时间内重复存档时合并，避免历史列表被同一份结果刷屏' },
    'data.dir': { label: '数据目录', hint: '历史、缓存、ASN 库都放在这里。留空按便携模式决定' },
    'data.portable': { label: '便携模式', hint: '数据放在程序同级目录，换台机器拷走就能接着用' },

    'export.csv_bom': { label: 'CSV 带 BOM', hint: '开着 Excel 打开不乱码；给脚本用时可以关掉' },
    'export.default_fields': { label: '默认字段集', hint: '导出时预选的字段组合' },
    'export.filename_template': { label: '文件名模板', hint: '可用占位符：时间、类型、条数' },

    'advanced.log_keep_days': { label: '日志保留天数', hint: '超过天数的日志会被清理' },
    'advanced.check_update': { label: '检查更新', hint: '启动时检查是否有新版本' },
  },
  en: {
    'ui.theme': { label: 'Theme', hint: 'Dark, light, or follow the system. Applies instantly' },
    'ui.lang': { label: 'Language', hint: 'Switch between Chinese and English' },
    'ui.font_scale': { label: 'Font size', hint: 'Small / medium / large, scaled globally without breaking layout' },
    'ui.table_density': { label: 'Table density', hint: 'Changes row height only, never the font size' },
    'ui.page_size': {
      label: 'Rows per page',
      hint: 'How many rows the result table shows per page. The table does not paginate yet, so this has no effect',
    },
    'ui.animation': { label: 'Animations', hint: 'Turning this off makes every transition instant. Also honours reduced-motion' },
    'ui.contrast': { label: 'High contrast', hint: 'Pushes text to the extremes, hardens borders and drops shadows. Independent of dark/light' },
    'ui.remember_state': {
      label: 'Remember UI state',
      hint: 'Reopen on the last page you were on and keep the parameter panel as you left it. Turned off, every launch starts on the start page; parameter values live in the config and are unaffected',
    },
    'ui.adaptive_enabled': { label: 'Smart adaptation', hint: 'Adjusts parameters to your network. Fills blanks only, never overwrites your values' },
    'ui.close_to_tray': { label: 'Minimise to tray on close', hint: 'Closing the window keeps it running in the background; use "Quit" in the tray menu to exit. Desktop build only' },
    'ui.adaptive_allow_preset': { label: 'Adaptation may change preset values', hint: 'When off, adaptation suggests instead of changing preset values' },

    'scan.workers': {
      label: 'Concurrency',
      hint: 'How many addresses at once. 50-100 on weak networks; above 300 strains the router. The ceiling comes from "Concurrency ceiling"',
    },
    'scan.sample_max': { label: 'Sample limit', hint: 'How many addresses to test. 500 is enough; 5000 is thorough but slower' },
    'scan.latency_threshold': { label: 'Latency limit', hint: 'Nodes above this latency are dropped' },
    'scan.ping_times': { label: 'Ping count', hint: 'Probes per address. 0 means automatic' },
    'scan.port': { label: 'Default port', hint: '443 by default. 2053 / 2083 are steadier on some networks' },
    'scan.timeout_ms': { label: 'Probe timeout', hint: 'How long one probe waits before failing' },
    'scan.retry': { label: 'Retries', hint: 'Retries after a failed probe' },
    'scan.mode': { label: 'Probe mode', hint: 'TCPing is faster; HTTPing is closer to real traffic but relaxes thresholds' },
    'scan.two_phase': { label: 'Two-phase scan', hint: 'Coarse pass first, then a fine pass on survivors. Faster' },
    'scan.verify_nodes': { label: 'Collect node details', hint: 'Off sends far fewer requests but drops region and operator info' },
    'scan.usability_check': { label: 'Usability check before speed test', hint: 'Confirms nodes are still alive right before testing' },

    'speed.concurrency': { label: 'Test concurrency', hint: 'How many downloads at once, 16 max. Too many triggers rate limits and skews the numbers' },
    'speed.target_qualified': { label: 'Qualified targets', hint: 'Stop after this many nodes pass' },
    'speed.min_speed': { label: 'Minimum speed', hint: 'Below this a node does not qualify' },
    'speed.url_mode': { label: 'Test source', hint: 'Auto picks by your ISP; you can also pin the official source or a custom URL' },
    'speed.custom_url': { label: 'Custom test URL', hint: 'Only used when the source is set to custom' },
    'speed.download_duration_s': { label: 'Test duration', hint: 'How long each node downloads' },
    'speed.breaker_429': { label: 'Rate-limit breaker', hint: 'Stop after this many consecutive rate limits. 1 stops on the first one' },

    'geo.asn_source': { label: 'ASN source', hint: 'iptoasn needs no account and is small; GeoLite2 is faster but larger. Off hides operator info' },
    'geo.asn_db_path': { label: 'Database location', hint: 'A directory or a file. A manually placed file takes precedence' },
    'geo.asn_auto_update': { label: 'Auto-update ASN database', hint: 'When off, no requests are made and the local copy is used as-is' },
    'geo.asn_update_interval_days': { label: 'Update interval (days)', hint: 'Update in the background after this many days. 0 means manual only' },
    'geo.geo_warn_enabled': { label: 'Proxy exit warning', hint: 'Shows a banner when your exit does not look domestic. Never blocks a task' },
    'geo.filter_asn': { label: 'Operator filter', hint: 'Only show nodes from certain operators. Empty means no filter' },

    'history.keep_count': { label: 'Keep count', hint: 'How many records to keep. Starred ones do not count' },
    'history.keep_mode': { label: 'Retention mode', hint: 'Clean up by count or by age' },
    'history.keep_days': { label: 'Keep days', hint: 'Used when retention mode is by age' },
    'history.auto_save': { label: 'Auto-save', hint: 'Save a record automatically when a task finishes' },

    'export.default_format': { label: 'Default format', hint: 'CSV includes a BOM so Excel opens it correctly' },

    'server.port': { label: 'Panel port', hint: 'Requires a restart. Older builds used 18543; this one standardises on 17443' },
    'server.bind': { label: 'Listen address', hint: 'Loopback keeps it to this machine. LAN lets any device on the network in — set an access token first' },
    'server.token': { label: 'Access token', hint: 'The password for LAN access. Empty disables the check, which is fine for loopback only' },
    'server.session_ttl_min': { label: 'Session lifetime (minutes)', hint: 'How long a sign-in lasts before the token is needed again' },
    'server.open_browser': { label: 'Open browser on start', hint: 'Saves typing the address when double-clicking. Use --no-browser during development' },
    'server.autostart': { label: 'Start on login', hint: 'Desktop build only' },

    'notify.on_done': { label: 'Notify on completion', hint: 'One notification when a scan or speed test finishes' },
    'notify.on_fail': { label: 'Notify on failure', hint: 'Notify when a task errors out' },
    'notify.web': { label: 'Browser notifications', hint: 'Needs browser permission; a denial is not asked for again' },
    'notify.tray': { label: 'Tray notifications', hint: 'Desktop build only' },
    'notify.sound': { label: 'Sound', hint: 'Off by default so it does not blurt out in public' },

    'advanced.log_level': { label: 'Log level', hint: 'Use debug when troubleshooting. Requires a restart' },
    'ui.density': { label: 'Parameter density', hint: 'Simple hides parameters by default; expanding once is remembered' },
    'ui.start_page': {
      label: 'Start page',
      hint: 'Which page to open on next launch. Every launch starts here when "Remember UI state" is off',
    },
    'ui.time_format': { label: 'Time display', hint: 'Local time or UTC' },

    'scan.source_mode': { label: 'Source mode', hint: 'Official ranges, custom sources, or both. The scan page sets this for you' },
    'scan.allowed_regions': { label: 'Region allowlist', hint: 'Keep only nodes in these regions; empty means no limit. Unknown regions are kept' },
    'scan.blocked_regions': { label: 'Region blocklist', hint: 'Drop nodes in these regions' },
    'scan.pre_filter_ports': { label: 'Pre-filter ports', hint: 'Drop candidates on these ports before any TCP probe, saving time' },

    'speed.interval_ms': { label: 'Test interval', hint: 'Wait this long between targets to reduce the chance of being rate limited' },
    'speed.max_download_mb': { label: 'Max download per node', hint: 'Stop testing a node past this amount. 0 means unlimited' },
    'speed.per_region_topn': { label: 'Top N per region', hint: 'Take the top N from each region. 0 disables the per-region split' },
    'speed.weight_speed': { label: 'Score weight · speed', hint: 'How much speed counts in the overall score' },
    'speed.weight_latency': { label: 'Score weight · latency', hint: 'How much latency counts in the overall score' },
    'speed.weight_jitter': { label: 'Score weight · jitter', hint: 'How much jitter counts. High jitter means inconsistent speed' },

    'source.merge_strategy': { label: 'Merge strategy', hint: 'Union takes every address; intersect keeps only those present in all sources' },
    'source.timeout_ms': { label: 'Fetch timeout', hint: 'How long a single remote source may take' },
    'source.retry': { label: 'Fetch retries', hint: 'Retries after a remote source fails' },
    'source.retry_interval_ms': { label: 'Retry interval', hint: 'Wait between retries' },

    'net.connect_timeout_ms': { label: 'Connect timeout', hint: 'How long establishing a TCP connection may take' },
    'net.use_tls': { label: 'TLS probing', hint: 'Auto infers from the port: 443 and 8443 use TLS, others do not' },
    'net.ip_version': { label: 'Address family', hint: 'IPv4 only, IPv6 only, or both' },
    'net.max_workers': {
      label: 'Concurrency ceiling',
      hint: 'The largest concurrency the panel will accept. 2000 by default; lower it to guard yourself, raise it to lift the ceiling. It is your own limit, not a system restriction',
    },
    'net.proxy': { label: 'HTTP proxy', hint: 'Used for official ranges and remote sources. Empty means direct' },
    'net.force_direct': { label: 'Force direct', hint: 'Ignore proxy settings from the environment. Node probing is always direct' },
    'net.custom_dns': { label: 'Custom DNS', hint: 'Resolvers used for hostnames; empty uses the system ones' },
    'net.dns_fallback': { label: 'DNS fallback', hint: 'Fall back to built-in public resolvers when system DNS fails' },
    'net.user_agent': { label: 'Request User-Agent', hint: 'UA used when fetching sources. Useful when a source blocks the default' },

    'history.auto_dedup': { label: 'Auto dedupe', hint: 'Merge records saved with the same parameters in a short window, so the list is not flooded' },
    'data.dir': { label: 'Data directory', hint: 'History, cache and the ASN database live here. Empty follows portable mode' },
    'data.portable': { label: 'Portable mode', hint: 'Keep data next to the binary so copying the folder moves everything' },

    'export.csv_bom': { label: 'CSV with BOM', hint: 'Keeps Excel from mangling the encoding; turn it off for scripts' },
    'export.default_fields': { label: 'Default field set', hint: 'The field set pre-selected when exporting' },
    'export.filename_template': { label: 'Filename template', hint: 'Placeholders available: time, type, count' },

    'advanced.log_keep_days': { label: 'Log retention (days)', hint: 'Logs older than this are cleaned up' },
    'advanced.check_update': { label: 'Check for updates', hint: 'Look for a newer version on startup' },
  },
}

/** 取一个字段的文案；缺文案时退回键本身，至少能看出是哪一项。 */
export function fieldText(path: string, locale: 'zh' | 'en'): FieldText {
  return settingsText[locale][path] ?? { label: path, hint: '' }
}

/** 组名。 */
export const groupText: Record<'zh' | 'en', Record<string, string>> = {
  zh: {
    ui: '界面',
    scan: '扫描',
    speed: '测速',
    source: '数据源',
    geo: 'ASN 与地理',
    history: '历史与数据',
    export: '导出',
    server: '服务面板',
    notify: '通知',
    net: '网络',
    advanced: '高级与调试',
  },
  en: {
    ui: 'Interface',
    scan: 'Scan',
    speed: 'Speed test',
    source: 'Sources',
    geo: 'ASN & geo',
    history: 'History & data',
    export: 'Export',
    server: 'Service panel',
    notify: 'Notifications',
    net: 'Network',
    advanced: 'Advanced',
  },
}

/** 读取嵌套配置里的一个值。 */
export function readPath(values: Record<string, unknown>, path: string): unknown {
  let current: unknown = values
  for (const part of path.split('.')) {
    if (current === null || typeof current !== 'object') return undefined
    current = (current as Record<string, unknown>)[part]
  }
  return current
}

/** 按点号路径构造一份嵌套的 patch，供 settings/update 使用。 */
export function buildPatch(path: string, value: unknown): Record<string, unknown> {
  const parts = path.split('.')
  const root: Record<string, unknown> = {}
  let cursor = root
  parts.forEach((part, index) => {
    if (index === parts.length - 1) {
      cursor[part] = value
      return
    }
    const next: Record<string, unknown> = {}
    cursor[part] = next
    cursor = next
  })
  return root
}
