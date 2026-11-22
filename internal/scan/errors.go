package scan

// 扫描失败的稳定错误码。
//
// 前端按码给可操作的建议，而不是匹配中文文案：文案改一个字，提示就会永远
// 不命中，而界面上还留着那句旧话——这类问题最难被发现。
const (
	// CodeNoCandidate 表示候选池是空的：来源全关、文本为空，或远程源全部拉不到。
	CodeNoCandidate = "E_NO_CANDIDATE"
	// CodeAllFiltered 表示候选有，但前置过滤（端口 / 地区）一条都没留下。
	CodeAllFiltered = "E_ALL_FILTERED"
)

// CodedError 是带稳定错误码的失败原因。
//
// 只给需要界面分支的失败用；参数校验之类的错误仍用普通 error，它们的处置
// 方式统一是「高亮字段」，不需要另分类。
type CodedError struct {
	Code string
	Msg  string
}

func (e *CodedError) Error() string { return e.Msg }
