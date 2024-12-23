package utils

import (
	"fmt"

	"github.com/cheggaaa/pb/v3"
)

type Bar struct {
	pb *pb.ProgressBar
	isDownload bool
}

func NewBar(count int, MyStrStart, MyStrEnd string) *Bar {
	var tmpl string
	isDownload := MyStrStart == "     " // 通过 MyStrStart 判断是否为下载测速进度条
	
	if isDownload {
		// 下载测速进度条，显示带宽
		tmpl = fmt.Sprintf(`{{counters . }} {{ bar . "[" "-" (cycle . "↖" "↗" "↘" "↙" ) "_" "]"}} {{string . "Bandwidth" | cyan}}`)
	} else {
		// 延迟测速进度条，显示可用数量
		tmpl = fmt.Sprintf(`{{counters . }} {{ bar . "[" "-" (cycle . "↖" "↗" "↘" "↙" ) "_" "]"}} %s {{string . "MyStr" | green}} %s`, MyStrStart, MyStrEnd)
	}
	
	bar := pb.ProgressBarTemplate(tmpl).Start(count)
	return &Bar{pb: bar, isDownload: isDownload}
}

func (b *Bar) Grow(num int, val string) {
	if b.isDownload {
		// 下载测速时显示带宽
		bandwidth := fmt.Sprintf("%.2f MB/s", GetCurrentBandwidth())
		b.pb.Set("Bandwidth", bandwidth).Add(num)
	} else {
		// 延迟测速时显示可用数量
		b.pb.Set("MyStr", val).Add(num)
	}
}

func (b *Bar) Done() {
	b.pb.Finish()
}
