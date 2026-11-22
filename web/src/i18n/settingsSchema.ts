/**
 * 设置项的结构表。
 *
 * 只描述**结构**（分组、键路径、控件类型、取值范围），文案另走 `settingsText`
 * 的中英两套映射——混在一起会让「改一句提示」变成改一个数据结构。
 *
 * 键路径用点号：`scan.workers`。与配置文件的嵌套结构、以及后端
 * `settings/reset` 接受的键一致，中间不做任何转换。
 */

export type FieldKind = 'bool' | 'int' | 'float' | 'text' | 'enum' | 'list' | 'path' | 'multi'

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
   * 只在另一项取到指定值时才显示。
   *
   * 互斥的几项一次只显示一个：并排摆着，用户得先判断「这项现在算数吗」。
   * 传数组表示同时满足。
   */
  showWhen?: FieldCondition | FieldCondition[]
  /**
   * 改了要重启才生效。
   *
   * 不拦着改——换机后第一件事就是改端口——但在行尾说清楚，别让人以为
   * 保存完就换过去了。
   */
  restart?: boolean
  /**
   * 一个控件背后是一组键，不是一个键。
   *
   * 选中某个取值时要写的键值：`{ precheck: { 'scan.verify_nodes': true, 'scan.usability_check': true } }`。
   * 界面上是一个选择，配置里仍是几个独立的布尔——迁移、历史记录、参数来源
   * 都照原字段走，不为界面好看去动存储。
   */
  writes?: Record<string, Record<string, unknown>>
  /**
   * 多选：每个选项对应一个布尔键，值 = 选中的选项集合。
   *
   * 与 writes 的区别是「可以同时选几个」：提醒方式能既弹通知又响一声，而
   * 节点明细的深浅只能挑一档。
   */
  toggles?: Record<string, string>
}

/** 一项显示条件：依赖项 path 的取值落在 in 里。 */
export interface FieldCondition {
  /** 依赖的配置项点号路径。 */
  path: string
  /** 取这些值之一时显示。 */
  in: unknown[]
}

export interface SettingGroup {
  id: string
  /**
   * 这一组实际覆盖的配置前缀，用于「恢复本组默认」。
   *
   * 界面上的分组按用途划，配置里的前缀按模块划，两者不必一一对应：数据源与
   * 网络是一起调的，拆成两组只会让人来回切。不给这个字段时取 id 本身。
   */
  members?: string[]
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
      { path: 'ui.font_scale', kind: 'enum', options: ['small', 'medium', 'large'], primary: true },
      { path: 'ui.animation', kind: 'bool', primary: true },
      { path: 'ui.contrast', kind: 'bool', primary: true },
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
      { path: 'scan.mode', kind: 'enum', options: ['tcping', 'httping'], primary: true },
      { path: 'scan.two_phase', kind: 'bool', primary: true },
      // 明细采集与测速前预检是同一件事的深浅两档：既然已经为每个节点发过请求，
      // 顺带预检不多花什么，拆成两个开关只会让人配出「采明细却不预检」的半截状态。
      {
        path: 'scan.node_check',
        kind: 'enum',
        options: ['off', 'detail', 'precheck'],
        writes: {
          off: { 'scan.verify_nodes': false, 'scan.usability_check': false },
          detail: { 'scan.verify_nodes': true, 'scan.usability_check': false },
          precheck: { 'scan.verify_nodes': true, 'scan.usability_check': true },
        },
      },
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
      { path: 'speed.min_speed', kind: 'int', min: 0, max: 1000, primary: true },
      { path: 'speed.url_mode', kind: 'enum', options: ['auto', 'official', 'mobile_friendly', 'mobile_only', 'custom'], primary: true },
      { path: 'speed.custom_url', kind: 'text', showWhen: { path: 'speed.url_mode', in: ['custom'] } },
      { path: 'speed.download_duration_s', kind: 'int', min: 1, max: 60 },
      // 至少熔断一次：0 会让限速一路测下去，拿到的速度全是假的。
      { path: 'speed.breaker_429', kind: 'int', min: 1, max: 100 },
      // 下限从 1 起：0 在请求里与「没带这个字段」无法区分，后端会把它当成
      // 未设置并补成 1.2 秒。与其让 0 静默变成别的数，不如不给这个选项——
      // 想要「几乎不等」填 1 即可，那个值会被原样采纳。
      { path: 'speed.interval_ms', kind: 'int', min: 1, max: 5000 },
      { path: 'speed.max_download_mb', kind: 'int', min: 0, max: 1000 },
      { path: 'speed.per_region_topn', kind: 'int', min: 0, max: 1000 },
      { path: 'speed.weight_speed', kind: 'float', min: 0, max: 10, step: 0.1 },
      { path: 'speed.weight_latency', kind: 'float', min: 0, max: 10, step: 0.1 },
      { path: 'speed.weight_jitter', kind: 'float', min: 0, max: 10, step: 0.1 },
    ],
  },
  {
    id: 'network',
    members: ['net', 'source'],
    fields: [
      { path: 'net.use_tls', kind: 'enum', options: ['auto', 'true', 'false'] },
      { path: 'net.ip_version', kind: 'enum', options: ['auto', 'v4', 'v6'] },
      { path: 'net.max_workers', kind: 'int', min: 1, max: 10000, primary: true },
      { path: 'net.proxy', kind: 'text' },
      { path: 'net.force_direct', kind: 'bool' },
      // 自定义 DNS 留空即用系统 DNS；填了而解析失败时总是回退系统，没有关掉的必要。
      { path: 'net.custom_dns', kind: 'list' },
      { path: 'net.user_agent', kind: 'text' },
      { path: 'source.merge_strategy', kind: 'enum', options: ['union', 'intersect'] },
      { path: 'source.timeout_ms', kind: 'int', min: 100, max: 60000, primary: true },
      { path: 'source.retry', kind: 'int', min: 0, max: 10 },
      { path: 'source.retry_interval_ms', kind: 'int', min: 0, max: 10000 },
    ],
  },
  {
    id: 'data',
    members: ['geo', 'history', 'data'],
    fields: [
      { path: 'geo.asn_source', kind: 'enum', options: ['iptoasn', 'geolite2_mmdb', 'off'], primary: true },
      {
        path: 'geo.asn_auto_update',
        kind: 'bool',
        showWhen: { path: 'geo.asn_source', in: ['iptoasn', 'geolite2_mmdb'] },
      },
      {
        path: 'geo.asn_update_interval_days',
        kind: 'int',
        min: 0,
        max: 365,
        showWhen: [
          { path: 'geo.asn_source', in: ['iptoasn', 'geolite2_mmdb'] },
          { path: 'geo.asn_auto_update', in: [true] },
        ],
      },
      { path: 'geo.asn_db_path', kind: 'path', showWhen: { path: 'geo.asn_source', in: ['geolite2_mmdb'] } },
      { path: 'geo.geo_warn_enabled', kind: 'bool' },
      { path: 'geo.filter_asn', kind: 'list' },
      // 保留方式必须可见：只显示其中一种数量而藏起开关，用户会以为另一项不存在。
      { path: 'history.keep_mode', kind: 'enum', options: ['count', 'days'], primary: true },
      {
        path: 'history.keep_count',
        kind: 'int',
        min: 1,
        max: 500,
        primary: true,
        showWhen: { path: 'history.keep_mode', in: ['count'] },
      },
      {
        path: 'history.keep_days',
        kind: 'int',
        min: 1,
        max: 3650,
        primary: true,
        showWhen: { path: 'history.keep_mode', in: ['days'] },
      },
      { path: 'history.auto_save', kind: 'bool' },
      { path: 'history.auto_dedup', kind: 'bool' },
      { path: 'data.dir', kind: 'path', restart: true },
      { path: 'data.portable', kind: 'bool', restart: true },
    ],
  },
  {
    id: 'export',
    members: ['export', 'notify'],
    fields: [
      { path: 'export.default_format', kind: 'enum', options: ['csv', 'json', 'txt'], primary: true },
      { path: 'export.default_fields', kind: 'enum', options: ['all', 'slim', 'ip_port'] },
      { path: 'export.filename_template', kind: 'text' },
      { path: 'notify.on_done', kind: 'bool' },
      { path: 'notify.on_fail', kind: 'bool' },
      {
        path: 'notify.channels',
        kind: 'multi',
        toggles: { web: 'notify.web', tray: 'notify.tray', sound: 'notify.sound' },
      },
    ],
  },
  {
    id: 'service',
    members: ['server', 'advanced'],
    fields: [
      { path: 'server.port', kind: 'int', min: 1, max: 65535, primary: true, restart: true },
      { path: 'server.bind', kind: 'enum', options: ['127.0.0.1', '0.0.0.0'], restart: true },
      { path: 'server.open_browser', kind: 'bool' },
      { path: 'server.autostart', kind: 'bool', restart: true },
      { path: 'server.session_ttl_min', kind: 'int', min: 1, max: 10080 },
      { path: 'advanced.log_level', kind: 'enum', options: ['debug', 'info', 'warn', 'error'], restart: true },
      { path: 'advanced.log_keep_days', kind: 'int', min: 1, max: 365 },
      { path: 'advanced.check_update', kind: 'bool' },
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
    'ui.animation': { label: '动效', hint: '关掉后所有过渡与动画变为瞬时。系统设置了减少动效时同样会关' },
    'ui.contrast': { label: '高对比度', hint: '把文字推到极值、边框加实、去掉投影。与深浅主题独立，可叠加使用' },
    'ui.adaptive_enabled': { label: '智能自适应', hint: '按网络环境自动调整参数。只填空白，绝不覆盖你改过的值' },
    'ui.close_to_tray': { label: '关闭窗口时收进托盘', hint: '关掉窗口后继续在后台跑；真要退出请用托盘菜单里的「退出」' },
    'ui.adaptive_allow_preset': { label: '自适应可改档位值', hint: '关掉后自适应只提示、不修改档位填进去的参数' },

    'scan.workers': {
      label: '并发',
      hint: '同时测多少个地址。弱网或老路由建议 50–100，超过 300 会明显加重路由器负担。上限由「并发上限」那一项决定',
    },
    'scan.sample_max': { label: '最多扫描 IP 数', hint: '从来源里最多挑多少个地址来测。500 够用，5000 更全面但更慢' },
    'scan.latency_threshold': { label: '延迟上限', hint: '超过这个延迟的节点直接淘汰' },
    'scan.ping_times': { label: '探测次数', hint: '每个地址测几次。0 表示自动' },
    'scan.port': { label: '默认端口', hint: '默认 443。部分网络下 2053 / 2083 更稳' },
    'scan.timeout_ms': { label: '探测超时', hint: '单次探测等多久算失败' },
    'scan.retry': { label: '失败重试', hint: '探测失败后重试几次' },
    'scan.mode': { label: '探测模式', hint: 'TCPing 更快，HTTPing 更接近真实访问但阈值会放宽' },
    'scan.two_phase': { label: '两阶段扫描', hint: '先低采样粗筛，再对达标节点精测。开着更快' },
    'scan.node_check': {
      label: '节点明细与预检',
      hint: '记下地区与运营商；选「记录并预检」会在测速前再确认一次节点还活着。关掉会少发大量请求、明显更快',
    },

    'speed.concurrency': { label: '测速并发', hint: '同时下载多少个，上限 16。太高容易触发限速，测出来的速度也不准' },
    'speed.target_qualified': { label: '收够多少合格即停', hint: '测到这么多个达标节点就结束本轮' },
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
    'server.bind': { label: '监听地址', hint: '仅本机时只有这台电脑能访问；局域网会让同网段的设备都能打开，所以要先在下面的「服务面板」里设好访问密码' },
    'server.session_ttl_min': { label: '会话有效期（分钟）', hint: '登录后多久需要重新输入密码' },
    'server.open_browser': { label: '启动后打开浏览器', hint: '双击运行时省去手动输地址。开发时可用 --no-browser 跳过' },
    'server.autostart': { label: '开机自启', hint: '登录系统时自动启动，省得每次手动打开' },

    'notify.on_done': { label: '任务完成提醒', hint: '扫描或测速结束时提示一次' },
    'notify.on_fail': { label: '任务失败提醒', hint: '任务出错时提示' },
    'notify.channels': {
      label: '提醒方式',
      hint: '可多选。浏览器通知需要授权，被拒绝时不会重复询问；托盘只在桌面版可用',
    },

    'advanced.log_level': { label: '日志级别', hint: '排查问题时调到 debug。改完需要重启才生效' },

    'scan.source_mode': { label: '来源模式', hint: '官方网段、自定义来源，或两者合并。扫描页会自动设置它' },
    'scan.allowed_regions': { label: '地区白名单', hint: '只保留这些地区的节点，留空表示不限。地区未知的会保守保留' },
    'scan.blocked_regions': { label: '地区黑名单', hint: '排除这些地区的节点' },
    'scan.pre_filter_ports': { label: '前置端口过滤', hint: '在 TCP 测试之前就排除这些端口的候选，省下探测时间' },

    'speed.interval_ms': { label: '测速间隔', hint: '每个目标之间等多久再测下一个，用来降低被限速的概率' },
    'speed.max_download_mb': { label: '单次最大下载量', hint: '超过就停止该节点的测速。0 表示不限' },
    'speed.per_region_topn': { label: '分地区配额', hint: '每个地区各取前几名参与测速。0 表示不按地区分配' },
    'speed.weight_speed': { label: '评分权重 · 速度', hint: '综合评分里速度占的比重' },
    'speed.weight_latency': { label: '评分权重 · 延迟', hint: '综合评分里延迟占的比重' },
    'speed.weight_jitter': { label: '评分权重 · 抖动', hint: '综合评分里抖动占的比重。抖动大意味着忽快忽慢' },

    'source.merge_strategy': { label: '多源合并方式', hint: '并集取所有来源的地址；交集只取同时出现在所有来源里的' },
    'source.timeout_ms': { label: '拉取超时', hint: '单个远端源等多久算失败' },
    'source.retry': { label: '拉取重试', hint: '远端源失败后重试几次' },
    'source.retry_interval_ms': { label: '重试间隔', hint: '两次重试之间等多久' },

    'net.use_tls': { label: 'TLS 探测', hint: '自动时按端口推断：443 与 8443 走 TLS，其余不走' },
    'net.ip_version': { label: '地址族', hint: '只测 IPv4、只测 IPv6，或两者都测' },
    'net.max_workers': {
      label: '并发上限',
      hint: '面板允许设到多大并发。默认 2000；调低它等于给自己加一道护栏，调高则放开。这是你自己定的上限，不是系统的限制',
    },
    'net.proxy': { label: 'HTTP 代理', hint: '拉取官方网段与远端源时走这个代理。留空表示直连' },
    'net.force_direct': { label: '强制直连', hint: '忽略环境变量里的代理设置。节点探测始终直连，不受这里影响' },
    'net.custom_dns': { label: '自定义 DNS', hint: '解析域名时用这些 DNS 服务器，留空用系统默认' },
    'net.user_agent': { label: '请求 User-Agent', hint: '拉取数据源时用的 UA。默认浏览器 UA，被拦时可能有用' },

    'history.auto_dedup': { label: '自动去重', hint: '同参数短时间内重复存档时合并，避免历史列表被同一份结果刷屏' },
    'data.dir': { label: '数据目录', hint: '历史、缓存、ASN 库都放在这里。留空按便携模式决定' },
    'data.portable': { label: '便携模式', hint: '数据放在程序同级目录，换台机器拷走就能接着用' },

    'export.default_fields': { label: '默认字段集', hint: '导出时预选的字段组合' },
    'export.filename_template': { label: '文件名模板', hint: '可用占位符：时间、类型、条数' },

    'advanced.log_keep_days': { label: '日志保留天数', hint: '超过天数的日志会被清理' },
    'advanced.check_update': { label: '自动检查更新', hint: '启动时问一次有没有新版本，结果显示在日志里。关掉之后一次请求都不发' },
  },
  en: {
    'ui.theme': { label: 'Theme', hint: 'Dark, light, or follow the system. Applies instantly' },
    'ui.lang': { label: 'Language', hint: 'Switch between Chinese and English' },
    'ui.font_scale': { label: 'Font size', hint: 'Small / medium / large, scaled globally without breaking layout' },
    'ui.animation': { label: 'Animations', hint: 'Turning this off makes every transition instant. Also honours reduced-motion' },
    'ui.contrast': { label: 'High contrast', hint: 'Pushes text to the extremes, hardens borders and drops shadows. Independent of dark/light' },
    'ui.adaptive_enabled': { label: 'Smart adaptation', hint: 'Adjusts parameters to your network. Fills blanks only, never overwrites your values' },
    'ui.close_to_tray': { label: 'Minimise to tray on close', hint: 'Closing the window keeps it running in the background; use "Quit" in the tray menu to exit' },
    'ui.adaptive_allow_preset': { label: 'Adaptation may change preset values', hint: 'When off, adaptation suggests instead of changing preset values' },

    'scan.workers': {
      label: 'Concurrency',
      hint: 'How many addresses at once. 50-100 on weak networks; above 300 strains the router. The ceiling comes from "Concurrency ceiling"',
    },
    'scan.sample_max': { label: 'Max addresses to scan', hint: 'How many addresses to pick from the sources. 500 is enough; 5000 is thorough but slower' },
    'scan.latency_threshold': { label: 'Latency ceiling', hint: 'Nodes above this latency are dropped' },
    'scan.ping_times': { label: 'Ping count', hint: 'Probes per address. 0 means automatic' },
    'scan.port': { label: 'Default port', hint: '443 by default. 2053 / 2083 are steadier on some networks' },
    'scan.timeout_ms': { label: 'Probe timeout', hint: 'How long one probe waits before failing' },
    'scan.retry': { label: 'Retries', hint: 'Retries after a failed probe' },
    'scan.mode': { label: 'Probe mode', hint: 'TCPing is faster; HTTPing is closer to real traffic but relaxes thresholds' },
    'scan.two_phase': { label: 'Two-phase scan', hint: 'Coarse pass first, then a fine pass on survivors. Faster' },
    'scan.node_check': {
      label: 'Node details and precheck',
      hint: 'Records region and operator; "Record and precheck" also re-confirms each node is alive before the speed test. Off sends far fewer requests and is much faster',
    },

    'speed.concurrency': { label: 'Test concurrency', hint: 'How many downloads at once, 16 max. Too many triggers rate limits and skews the numbers' },
    'speed.target_qualified': { label: 'Stop after N qualified', hint: 'Ends the run once this many nodes pass' },
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
    'server.bind': { label: 'Listen address', hint: 'Loopback keeps it to this machine. LAN lets any device on the network in, so set an access password in the service panel below first' },
    'server.session_ttl_min': { label: 'Session lifetime (minutes)', hint: 'How long a sign-in lasts before the password is needed again' },
    'server.open_browser': { label: 'Open browser on start', hint: 'Saves typing the address when double-clicking. Use --no-browser during development' },
    'server.autostart': { label: 'Start on login', hint: 'Launches itself when you sign in, so you do not have to open it by hand' },

    'notify.on_done': { label: 'Notify on completion', hint: 'One notification when a scan or speed test finishes' },
    'notify.on_fail': { label: 'Notify on failure', hint: 'Notify when a task errors out' },
    'notify.channels': {
      label: 'Notify via',
      hint: 'Pick any. Browser notifications need permission and are not asked for again after a denial; tray is desktop only',
    },

    'advanced.log_level': { label: 'Log level', hint: 'Use debug when troubleshooting. Requires a restart' },

    'scan.source_mode': { label: 'Source mode', hint: 'Official ranges, custom sources, or both. The scan page sets this for you' },
    'scan.allowed_regions': { label: 'Region allowlist', hint: 'Keep only nodes in these regions; empty means no limit. Unknown regions are kept' },
    'scan.blocked_regions': { label: 'Region blocklist', hint: 'Drop nodes in these regions' },
    'scan.pre_filter_ports': { label: 'Pre-filter ports', hint: 'Drop candidates on these ports before any TCP probe, saving time' },

    'speed.interval_ms': { label: 'Test interval', hint: 'Wait this long between targets to reduce the chance of being rate limited' },
    'speed.max_download_mb': { label: 'Max download per node', hint: 'Stop testing a node past this amount. 0 means unlimited' },
    'speed.per_region_topn': { label: 'Per-region quota', hint: 'Take the top N from each region. 0 disables the per-region split' },
    'speed.weight_speed': { label: 'Score weight · speed', hint: 'How much speed counts in the overall score' },
    'speed.weight_latency': { label: 'Score weight · latency', hint: 'How much latency counts in the overall score' },
    'speed.weight_jitter': { label: 'Score weight · jitter', hint: 'How much jitter counts. High jitter means inconsistent speed' },

    'source.merge_strategy': { label: 'Merge strategy', hint: 'Union takes every address; intersect keeps only those present in all sources' },
    'source.timeout_ms': { label: 'Fetch timeout', hint: 'How long a single remote source may take' },
    'source.retry': { label: 'Fetch retries', hint: 'Retries after a remote source fails' },
    'source.retry_interval_ms': { label: 'Retry interval', hint: 'Wait between retries' },
    'net.use_tls': { label: 'TLS probing', hint: 'Auto infers from the port: 443 and 8443 use TLS, others do not' },
    'net.ip_version': { label: 'Address family', hint: 'IPv4 only, IPv6 only, or both' },
    'net.max_workers': {
      label: 'Concurrency ceiling',
      hint: 'The largest concurrency the panel will accept. 2000 by default; lower it to guard yourself, raise it to lift the ceiling. It is your own limit, not a system restriction',
    },
    'net.proxy': { label: 'HTTP proxy', hint: 'Used for official ranges and remote sources. Empty means direct' },
    'net.force_direct': { label: 'Force direct', hint: 'Ignore proxy settings from the environment. Node probing is always direct' },
    'net.custom_dns': { label: 'Custom DNS', hint: 'Resolvers used for hostnames; empty uses the system ones' },
    'net.user_agent': { label: 'Request User-Agent', hint: 'UA used when fetching sources. Useful when a source blocks the default' },

    'history.auto_dedup': { label: 'Auto dedupe', hint: 'Merge records saved with the same parameters in a short window, so the list is not flooded' },
    'data.dir': { label: 'Data directory', hint: 'History, cache and the ASN database live here. Empty follows portable mode' },
    'data.portable': { label: 'Portable mode', hint: 'Keep data next to the binary so copying the folder moves everything' },

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
    network: '网络与来源',
    data: '数据与归属地',
    export: '导出与提醒',
    service: '服务与高级',
  },
  en: {
    ui: 'Interface',
    scan: 'Scan',
    speed: 'Speed test',
    network: 'Network & sources',
    data: 'Data & geo',
    export: 'Export & alerts',
    service: 'Service & advanced',
  },
}

/**
 * 一项在当前配置下是否该显示。
 *
 * 只依赖配置值本身，不看别的状态：条件与取值不一致时（例如保留方式已切到按
 * 天数、条数项却还显示），用户改的值会和界面对不上。
 */
export function fieldVisible(field: SettingField, values: Record<string, unknown>): boolean {
  if (field.showWhen === undefined) return true
  const conditions = Array.isArray(field.showWhen) ? field.showWhen : [field.showWhen]
  return conditions.every((condition) => condition.in.includes(readPath(values, condition.path)))
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

/**
 * 一项当前的值。
 *
 * 普通项直接读路径；合成项（writes / toggles）没有自己的存储，要从它背后的
 * 几个键反推——否则界面会显示成空，用户一碰就把原来的配置覆盖掉。
 */
export function fieldValue(field: SettingField, values: Record<string, unknown>): unknown {
  if (field.toggles !== undefined) {
    const { toggles } = field
    return Object.keys(toggles).filter((option) => readPath(values, toggles[option]!) === true)
  }
  if (field.writes !== undefined) {
    for (const [option, spec] of Object.entries(field.writes)) {
      if (Object.entries(spec).every(([path, want]) => readPath(values, path) === want)) return option
    }
    // 几个键的取值凑不出任何一档（例如手工改过配置文件），显示成未选而不是
    // 猜一个最近的——猜错的话用户下次点保存就把它固化下来了。
    return ''
  }
  return readPath(values, field.path)
}

/**
 * 一次改动要写回的键值，键是点号路径。
 *
 * 合成项按选项展开成多个键；普通项就是它自己。
 */
export function fieldPatch(field: SettingField, value: unknown): Record<string, unknown> {
  if (field.toggles !== undefined) {
    const chosen = Array.isArray(value) ? value.map(String) : []
    const patch: Record<string, unknown> = {}
    for (const [option, path] of Object.entries(field.toggles)) {
      patch[path] = chosen.includes(option)
    }
    return patch
  }
  if (field.writes !== undefined) return field.writes[String(value)] ?? {}
  return { [field.path]: value }
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

/** 把若干「点号路径 → 值」合成一份嵌套 patch；同前缀的键合并而不是互相覆盖。 */
export function buildPatchMany(pairs: Record<string, unknown>): Record<string, unknown> {
  const root: Record<string, unknown> = {}
  for (const [path, value] of Object.entries(pairs)) {
    mergeInto(root, buildPatch(path, value))
  }
  return root
}

/**
 * 一项是否命中搜索。
 *
 * 三个维度都算：名称、配置路径、说明。只按名称匹配的话，用户记得「那个 429
 * 的开关」却记不住它叫「限速熔断阈值」就搜不到；路径是给按配置文件排查的人
 * 准备的；说明里往往写着代价与适用场景，正是要找的东西。
 */
export function fieldMatches(field: SettingField, query: string, locale: 'zh' | 'en'): boolean {
  const needle = query.trim().toLowerCase()
  if (needle === '') return true
  const text = fieldText(field.path, locale)
  return [field.path, text.label, text.hint].some((value) => value.toLowerCase().includes(needle))
}

export interface TextPart {
  text: string
  hit: boolean
}

/** 把一段文案按查询词切开，命中的片段单独标出来。 */
export function highlightParts(value: string, query: string): TextPart[] {
  const needle = query.trim().toLowerCase()
  if (needle === '') return [{ text: value, hit: false }]
  const lower = value.toLowerCase()
  const parts: TextPart[] = []
  let from = 0
  for (;;) {
    const at = lower.indexOf(needle, from)
    if (at < 0) break
    if (at > from) parts.push({ text: value.slice(from, at), hit: false })
    parts.push({ text: value.slice(at, at + needle.length), hit: true })
    from = at + needle.length
  }
  if (from < value.length) parts.push({ text: value.slice(from), hit: false })
  return parts.length === 0 ? [{ text: value, hit: false }] : parts
}

function mergeInto(into: Record<string, unknown>, from: Record<string, unknown>): void {
  for (const [key, value] of Object.entries(from)) {
    const current = into[key]
    if (current !== null && typeof current === 'object' && !Array.isArray(current) && value !== null && typeof value === 'object' && !Array.isArray(value)) {
      mergeInto(current as Record<string, unknown>, value as Record<string, unknown>)
    } else {
      into[key] = value
    }
  }
}
