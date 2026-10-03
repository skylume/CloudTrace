package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"math"
	"os"
	"sort"
	"strings"
	"sync"

	"cloudtrace/internal/atomicfile"
	"cloudtrace/internal/model"
)

/**
 * 档位：一批参数值的快照。
 *
 * 档位不是一道关卡，而是「批量填一组值」——填完立刻可以改。因此这里的每个
 * 方法都只处理值的搬运，不负责决定用户该用哪个档位。
 */

// 内置档位 id。「自定义」不是一个档位，而是「现在的值对不上任何档位」这个
// 状态的显示名，因此它不在这里，也不入库。
const (
	PresetFast     = "fast"
	PresetStandard = "standard"
	PresetPrecise  = "precise"
)

// Preset 是一个档位。
//
// Values 用配置里的点号路径作键（`scan.workers`），与参数来源表的键同形：两
// 份表用同一套键，「这一项是从哪个档位来的」才对得上。
type Preset struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Note    string `json:"note,omitempty"`
	Builtin bool   `json:"builtin"` // 内置档位只读：不可改、不可删
	Icon    string `json:"icon,omitempty"`
	Color   string `json:"color,omitempty"`
	/** Order 决定排列；小的在前。排序 / 置顶都靠它。 */
	Order   int                `json:"order"`
	Values  map[string]any     `json:"values"`
	Origins model.ParamOrigins `json:"origins,omitempty"`
}

/**
 * BuiltinPresets 返回三个内置档位。
 *
 * 「标准」档故意不设探测次数：那一列在界面上写的是「自动」，而「自动」的意
 * 思就是不给这一项填值——填了 0 会被当成非法参数，填任何实数都等于替用户做
 * 了决定。
 *
 * 内置档位不落盘：它们由代码定义，改了代码就该生效，留一份用户拷贝只会让两
 * 边不一致。
 */
func BuiltinPresets() []Preset {
	return []Preset{
		{
			ID:      PresetFast,
			Name:    "快速",
			Note:    "1 分钟内出结果",
			Builtin: true,
			Values: map[string]any{
				"scan.sample_max":        500,
				"scan.workers":           100,
				"scan.latency_threshold": 300,
				"scan.ping_times":        1,
				"speed.target_qualified": 5,
			},
		},
		{
			ID:      PresetStandard,
			Name:    "标准",
			Note:    "绝大多数场景",
			Builtin: true,
			Order:   1,
			Values: map[string]any{
				"scan.sample_max":        2000,
				"scan.workers":           150,
				"scan.latency_threshold": 230,
				"speed.target_qualified": 10,
			},
		},
		{
			ID:      PresetPrecise,
			Name:    "精细",
			Note:    "想更全面时",
			Builtin: true,
			Order:   2,
			Values: map[string]any{
				"scan.sample_max":        5000,
				"scan.workers":           200,
				"scan.latency_threshold": 200,
				"scan.ping_times":        3,
				"speed.target_qualified": 20,
			},
		},
	}
}

// ---- 存储 ----

// presetsFile 是档位文件的形状。
//
// 只有自定义档位入库：内置档位由代码定义，写进文件反而会在代码改动后留下一
// 份过期的拷贝。
type presetsFile struct {
	Default string   `json:"default"`
	Presets []Preset `json:"presets"`
}

// PresetStore 是自定义档位的唯一数据源。
//
// 与配置分开存放：档位是用户数据（像历史那样），不是设置项。混进配置文件会
// 让 settings/update 的补丁有机会碰到它。
type PresetStore struct {
	mu        sync.RWMutex
	path      string
	custom    []Preset
	defaultID string
	warns     []string
	logger    *slog.Logger
}

// OpenPresets 打开（或初始化）档位库。
//
// 文件损坏时不返回错误、也不立刻覆盖：先用内置档位跑起来，把原因记进
// Warnings()，等用户下一次保存时再原子地写回。档位是辅助功能，不该因为它
// 打不开就整个程序起不来。
func OpenPresets(path string, logger *slog.Logger) (*PresetStore, error) {
	if path == "" {
		return nil, errors.New("档位文件路径不能为空")
	}
	if logger == nil {
		logger = slog.Default()
	}
	s := &PresetStore{path: path, defaultID: PresetFast, logger: logger}

	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		var file presetsFile
		if jerr := json.Unmarshal(data, &file); jerr != nil {
			s.warns = append(s.warns, fmt.Sprintf("档位文件损坏，已按内置档位启动：%v", jerr))
			return s, nil
		}
		for _, p := range file.Presets {
			// 内置档位不该出现在文件里：那是旧版本写进去的，重新生成即可。
			if p.Builtin || p.ID == "" {
				continue
			}
			normalizeValues(p.Values)
			s.custom = append(s.custom, p)
		}
		if file.Default != "" {
			s.defaultID = file.Default
		}
	case errors.Is(err, fs.ErrNotExist):
		// 首次运行：用内置默认档位，不写文件，等真的有东西要存时再写。
	default:
		return nil, fmt.Errorf("读取档位文件失败：%w", err)
	}
	return s, nil
}

// Warnings 返回启动期的非致命问题。
func (s *PresetStore) Warnings() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string(nil), s.warns...)
}

// List 返回全部档位：内置在前，自定义在后，各自按 Order 再按名称排。
//
// 前端按 Builtin 分成两组，因此这里只要保证组内的顺序稳定。
func (s *PresetStore) List() []Preset {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]Preset, 0, len(s.custom)+len(BuiltinPresets()))
	out = append(out, BuiltinPresets()...)
	out = append(out, s.custom...)
	sort.SliceStable(out, func(i, j int) bool {
		// 内置永远排在自定义前面：界面按这个顺序分成两组，混排会让「内置档位」
		// 那一组里冒出一个用户的档位。
		if rank := rankOf(out[i]) - rankOf(out[j]); rank != 0 {
			return rank < 0
		}
		if out[i].Order != out[j].Order {
			return out[i].Order < out[j].Order
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// rankOf 给出分组序号：内置在前，自定义在后。
func rankOf(p Preset) int {
	if p.Builtin {
		return 0
	}
	return 1
}

// Get 按 id 取一个档位（内置与自定义都算）。
func (s *PresetStore) Get(id string) (Preset, bool) {
	for _, p := range BuiltinPresets() {
		if p.ID == id {
			return p, true
		}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, p := range s.custom {
		if p.ID == id {
			return p, true
		}
	}
	return Preset{}, false
}

// Default 返回启动档位 id。
//
// 取不到（档位被删了、文件里写了个不存在的 id）时退回内置默认，而不是返回
// 空——空 id 会让界面上「当前档位」那一项显示成空白。
func (s *PresetStore) Default() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if _, ok := s.lookup(s.defaultID); ok {
		return s.defaultID
	}
	return PresetFast
}

// lookup 查一个 id 是否存在。调用方必须持有锁。
func (s *PresetStore) lookup(id string) (Preset, bool) {
	for _, p := range BuiltinPresets() {
		if p.ID == id {
			return p, true
		}
	}
	for _, p := range s.custom {
		if p.ID == id {
			return p, true
		}
	}
	return Preset{}, false
}

// SetDefault 把某个档位设为启动档位。
func (s *PresetStore) SetDefault(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.lookup(id); !ok {
		return unknownPreset(id)
	}
	if s.defaultID == id {
		return nil
	}
	s.defaultID = id
	return s.persist()
}

// Save 新建或覆盖保存一个自定义档位。
//
// 内置档位的 id 是保留的：覆盖它们会让「内置档位只读」这条规则从界面上消失
// ——用户只是想存一份自己的，不该把系统预设也顶掉。
func (s *PresetStore) Save(p Preset) error {
	p.ID = strings.TrimSpace(p.ID)
	if err := validatePreset(p); err != nil {
		return err
	}
	for _, builtin := range BuiltinPresets() {
		if p.ID == builtin.ID {
			return &ValidationError{Fields: []FieldError{
				{Key: "id", Value: p.ID, Reason: "内置档位的标识是保留的，请换一个标识或另存为副本"},
			}}
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	p.Builtin = false
	normalizeValues(p.Values)
	for i, existing := range s.custom {
		if existing.ID == p.ID {
			s.custom[i] = p
			return s.persist()
		}
	}
	s.custom = append(s.custom, p)
	return s.persist()
}

// Delete 删除一个自定义档位。
//
// 删掉的正好是启动档位时，启动档位退回内置默认：留着一个指向不存在的档位的
// id，界面上「当前档位」会是空的。
func (s *PresetStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx := -1
	for i, p := range s.custom {
		if p.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		// 内置档位在这里必然找不到，因此「删内置档位」也走这条分支。
		return unknownPreset(id)
	}
	s.custom = append(s.custom[:idx], s.custom[idx+1:]...)
	if s.defaultID == id {
		s.defaultID = PresetFast
	}
	return s.persist()
}

/**
 * Apply 把档位展开成一份配置补丁与来源标记。
 *
 * 来源标记是这里最关键的一步：**「我的档位」载入的参数一律标记为 user**。
 * 用户保存档位时表达的是「我就要这组值」，自适应逻辑不该再动它们；内置档位
 * 填进去的值才标记成 preset，允许自适应按网络环境调整。
 */
func (s *PresetStore) Apply(id string) (map[string]any, model.ParamOrigins, error) {
	preset, ok := s.Get(id)
	if !ok {
		return nil, nil, unknownPreset(id)
	}

	origin := model.OriginPreset
	if !preset.Builtin {
		origin = model.OriginUser
	}

	patch := map[string]any{}
	origins := model.ParamOrigins{}
	for key, value := range preset.Values {
		path := splitKey(key)
		setPath(patch, path, value)
		// 键用规范化后的路径写回来源表：来源表与配置补丁必须是同一套键，
		// 否则「这一项来自档位」的判断会落空。
		origins.Set(strings.Join(path, "."), origin)
	}
	return patch, origins, nil
}

// ---- 内部 ----

/**
 * normalizeValues 把 JSON 读回来的整数还原成整数。
 *
 * encoding/json 把数字一律读成 float64，写着 60 的并发值取出来是 60（浮点）。
 * 配置那边能照常吃下，但「存进去的是 60」和「取出来不是 60」不一致，比较与
 * 显示都会别扭。小数原样保留——像 speed.min_speed 的 6.5 必须是小数。
 */
func normalizeValues(values map[string]any) {
	for key, value := range values {
		f, ok := value.(float64)
		if !ok || math.IsInf(f, 0) || math.IsNaN(f) || f != math.Trunc(f) {
			continue
		}
		values[key] = int(f)
	}
}

// validatePreset 校验一个待保存的档位。
func validatePreset(p Preset) error {
	if p.ID == "" {
		return &ValidationError{Fields: []FieldError{{Key: "id", Reason: "档位标识不能为空"}}}
	}
	if strings.TrimSpace(p.Name) == "" {
		return &ValidationError{Fields: []FieldError{{Key: "name", Value: p.Name, Reason: "档位名称不能为空"}}}
	}
	if len(p.Values) == 0 {
		return &ValidationError{Fields: []FieldError{{Key: "values", Reason: "档位至少要包含一个参数"}}}
	}

	// 每个键都必须是配置认识的路径。认不出的键写进去会被静默丢掉，用户保存
	// 完发现参数没生效，却不知道是哪一个键写错了。
	def, err := toObject(Default())
	if err != nil {
		return err
	}
	fields := make([]FieldError, 0)
	for _, key := range sortedKeys(p.Values) {
		if _, ok := lookupPath(def, splitKey(key)); !ok {
			fields = append(fields, FieldError{Key: key, Value: p.Values[key], Reason: "没有这个配置项"})
		}
	}
	if len(fields) > 0 {
		return &ValidationError{Fields: fields}
	}

	// 再把这组值套到默认配置上跑一遍校验：类型不对与超出范围都能在这里拦下。
	//
	// 只在保存时查一次是不够的——等到应用档位才发现值不合法，用户已经存了一份
	// 用不了的档位，而且报错会出现在「切换档位」这个与保存毫不相干的动作上。
	probe := def
	for _, key := range sortedKeys(p.Values) {
		setPath(probe, splitKey(key), p.Values[key])
	}
	next, err := fromObject(probe)
	if err != nil {
		return &ValidationError{Fields: []FieldError{{Key: "values", Value: p.Values, Reason: "参数值的类型不对：" + err.Error()}}}
	}
	if err := next.Validate(); err != nil {
		return err
	}
	return nil
}

// sortedKeys 让校验错误按固定顺序出现，而不是随 map 遍历顺序变。
func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// persist 原子写回文件。调用方必须持有写锁。
func (s *PresetStore) persist() error {
	file := presetsFile{Default: s.defaultID, Presets: s.custom}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化档位失败：%w", err)
	}
	data = append(data, '\n')
	if err := atomicfile.Write(s.path, data, filePerm); err != nil {
		return fmt.Errorf("写入档位文件失败：%w", err)
	}
	return nil
}

func unknownPreset(id string) error {
	return &ValidationError{Fields: []FieldError{
		{Key: "id", Value: id, Reason: "没有这个档位"},
	}}
}
