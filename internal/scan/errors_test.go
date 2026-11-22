package scan

import (
	"errors"
	"testing"
)

// 失败码要能被上层取到：只靠 Error() 的文本，界面就只能匹配中文。
func TestCodedErrorCarriesCode(t *testing.T) {
	var err error = &CodedError{Code: CodeAllFiltered, Msg: "前置过滤后没有剩余候选"}

	var coded *CodedError
	if !errors.As(err, &coded) {
		t.Fatal("errors.As 未能识别 *CodedError")
	}
	if coded.Code != CodeAllFiltered {
		t.Errorf("code = %q，期望 %q", coded.Code, CodeAllFiltered)
	}
	if err.Error() != "前置过滤后没有剩余候选" {
		t.Errorf("Error() = %q", err.Error())
	}
}

// 两个码必须不同：前端靠它们给出不同的下一步动作。
func TestScanErrorCodesAreDistinct(t *testing.T) {
	if CodeNoCandidate == CodeAllFiltered {
		t.Fatalf("两个失败码相同：%q", CodeNoCandidate)
	}
}
