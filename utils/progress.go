package utils

import (
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/term"
)

type Bar struct {
	current    int
	total      int
	prefix     string
	suffix     string
	finished   bool
	startTime  time.Time
	lastUpdate time.Time
	bandwidth  float64
}

func NewBar(total int, prefix string, suffix string) *Bar {
	return &Bar{
		total:     total,
		prefix:    prefix,
		suffix:    suffix,
		startTime: time.Now(),
	}
}

func (b *Bar) SetBandwidth(bw float64) {
	b.bandwidth = bw
}

func (b *Bar) getTerminalWidth() int {
	width, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || width <= 0 {
		return 80 // 默认终端宽度
	}
	return width
}

func (b *Bar) Grow(n int, suffix string) {
	b.current += n
	b.suffix = suffix
	
	now := time.Now()
	if !b.finished && (now.Sub(b.lastUpdate) > time.Millisecond*50 || b.current >= b.total) {
		b.lastUpdate = now
		
		// 获取终端宽度
		termWidth := b.getTerminalWidth()
		
		// 计算其他部分占用的宽度
		var otherWidth int
		if b.bandwidth > 0 {
			otherWidth = len(fmt.Sprintf("%d / %d [] %.2f MB/s", b.current, b.total, b.bandwidth))
		} else {
			otherWidth = len(fmt.Sprintf("%d / %d [] %s", b.current, b.total, b.suffix))
		}
		
		// 计算进度条可用宽度
		barWidth := termWidth - otherWidth - 1
		if barWidth < 10 {
			barWidth = 10
		}
		
		// 计算进度
		percent := float64(b.current) / float64(b.total)
		filled := int(percent * float64(barWidth))
		if filled > barWidth {
			filled = barWidth
		}
		
		// 渐变效果字符，从粗到细
		blocks := []string{"█", "▇", "▆", "▅", "▄", "▃", "▂", "▁"}
		
		// 构建进度条
		var bar string
		if filled < barWidth {
			// 已完成部分
			bar = strings.Repeat("█", filled)
			
			// 添加渐变动画
			elapsed := time.Since(b.startTime).Seconds()
			blockIndex := int(elapsed*15) % len(blocks)
			bar += blocks[blockIndex]
			
			// 未完成部分
			bar += strings.Repeat("░", barWidth-filled-1)
		} else {
			bar = strings.Repeat("█", barWidth)
		}
		
		// 格式化输出
		if b.bandwidth > 0 {
			fmt.Printf("\r%d / %d [%s] %.2f MB/s",
				b.current,
				b.total,
				 bar,
				b.bandwidth,
			)
		} else {
			fmt.Printf("\r%d / %d [%s] %s",
				b.current,
				b.total,
				 bar,
				b.suffix,
			)
		}
		
		if b.current >= b.total {
			b.finished = true
			fmt.Println()
		}
	}
}

func (b *Bar) Done() {
	if !b.finished {
		b.finished = true
		fmt.Println()
	}
}
