package utils

import (
	"encoding/csv"
	"fmt"
	"log"
	"math/rand"
	"net"
	"os"
	"sort"
	"strconv"
	"time"
	"sync/atomic"
)

const (
	defaultOutput         = "result.csv"
	maxDelay              = 9999 * time.Millisecond
	minDelay              = 0 * time.Millisecond
	maxLossRate   float32 = 1.0
)

var (
	InputMaxDelay    = maxDelay
	InputMinDelay    = minDelay
	InputMaxLossRate = maxLossRate
	Output           = defaultOutput
	PrintNum         = 10
	currentBandwidth int64
)

// 带宽相关函数
func GetCurrentBandwidth() float64 {
	return float64(atomic.LoadInt64(&currentBandwidth)) / 1024 / 1024
}

func UpdateBandwidth(speed int64) {
	atomic.StoreInt64(&currentBandwidth, speed)
}

// 是否打印测试结果
func NoPrintResult() bool {
	return PrintNum == 0
}

// 是否输出到文件
func noOutput() bool {
	return Output == "" || Output == " "
}

type PingData struct {
	IP       *net.IPAddr
	Sended   int
	Received int
	Delay    time.Duration
}

type CloudflareIPData struct {
	*PingData
	lossRate      float32
	DownloadSpeed float64
	datacenter    string
}

// 计算丢包率
func (cf *CloudflareIPData) getLossRate() float32 {
	if cf.lossRate == 0 {
		pingLost := cf.Sended - cf.Received
		cf.lossRate = float32(pingLost) / float32(cf.Sended)
	}
	return cf.lossRate
}

func (cf *CloudflareIPData) toString() []string {
	result := make([]string, 7)
	result[0] = cf.IP.String()
	result[1] = strconv.Itoa(cf.Sended)
	result[2] = strconv.Itoa(cf.Received)
	result[3] = strconv.FormatFloat(float64(cf.getLossRate()), 'f', 2, 32)
	result[4] = strconv.FormatFloat(cf.Delay.Seconds()*1000, 'f', 2, 32)
	result[5] = strconv.FormatFloat(cf.DownloadSpeed/1024/1024, 'f', 2, 32)
	result[6] = cf.datacenter
	return result
}

func ExportCsv(data []CloudflareIPData) {
	if noOutput() || len(data) == 0 {
		return
	}
	
	fp, err := os.Create(Output)
	if err != nil {
		log.Fatalf("创建文件[%s]失败：%v", Output, err)
		return
	}
	defer fp.Close()
	w := csv.NewWriter(fp)
	_ = w.Write([]string{"IP 地址", "已发送", "已接收", "丢包率", "平均延迟", "下载速度 (MB/s)", "数据中心"})
	_ = w.WriteAll(convertToString(data))
	w.Flush()
}

func convertToString(data []CloudflareIPData) [][]string {
	result := make([][]string, 0)
	for _, v := range data {
		result = append(result, v.toString())
	}
	return result
}

// 延迟丢包排序
type PingDelaySet []CloudflareIPData

// 延迟分组，每组内延迟差距在 5ms 以内
type DelayGroup struct {
	minDelay time.Duration
	maxDelay time.Duration
	ips      []CloudflareIPData
}

// 对延迟测速结果进行分组并随机化
func (s PingDelaySet) GroupAndShuffle() PingDelaySet {
	if len(s) == 0 {
		return s
	}

	// 先按延迟排序
	sort.Sort(s)

	// 创建延迟分组
	var groups []DelayGroup
	currentGroup := DelayGroup{
		minDelay: s[0].Delay,
		maxDelay: s[0].Delay,
		ips:      []CloudflareIPData{s[0]},
	}

	// 按照 5ms 间隔分组
	for i := 1; i < len(s); i++ {
		if s[i].Delay-currentGroup.minDelay <= 5*time.Millisecond {
			currentGroup.ips = append(currentGroup.ips, s[i])
			if s[i].Delay > currentGroup.maxDelay {
				currentGroup.maxDelay = s[i].Delay
			}
		} else {
			groups = append(groups, currentGroup)
			currentGroup = DelayGroup{
				minDelay: s[i].Delay,
				maxDelay: s[i].Delay,
				ips:      []CloudflareIPData{s[i]},
			}
		}
	}
	groups = append(groups, currentGroup)

	// 打乱每组内的顺序
	result := make(PingDelaySet, 0, len(s))
	for _, group := range groups {
		// Fisher-Yates 洗牌算法
		for i := len(group.ips) - 1; i > 0; i-- {
			j := rand.Intn(i + 1)
			group.ips[i], group.ips[j] = group.ips[j], group.ips[i]
		}
		result = append(result, group.ips...)
	}

	return result
}

// 延迟条件过滤
func (s PingDelaySet) FilterDelay() (data PingDelaySet) {
	if InputMaxDelay > maxDelay || InputMinDelay < minDelay { // 当输入的延迟条件不在默认范围内时，不进行过滤
		return s.GroupAndShuffle()
	}
	if InputMaxDelay == maxDelay && InputMinDelay == minDelay { // 当输入的延迟条件为默认值时，不进行过滤
		return s.GroupAndShuffle()
	}
	for _, v := range s {
		if v.Delay > InputMaxDelay { // 平均延迟上限，延迟大于条件最大值时，后面的数据都不满足条件，直接跳出循环
			break
		}
		if v.Delay < InputMinDelay { // 平均延迟下限，延迟小于条件最小值时，不满足条件，跳过
			continue
		}
		data = append(data, v) // 延迟满足条件时，添加到新数组中
	}
	return data.GroupAndShuffle()
}

// 丢包条件过滤
func (s PingDelaySet) FilterLossRate() (data PingDelaySet) {
	if InputMaxLossRate >= maxLossRate { // 当输入的丢包条件为默认值时，不进行过滤
		return s
	}
	for _, v := range s {
		if v.getLossRate() > InputMaxLossRate { // 丢包几率上限
			break
		}
		data = append(data, v) // 丢包率满足条件时，添加到新数组中
	}
	return
}

func (s PingDelaySet) Len() int {
	return len(s)
}
func (s PingDelaySet) Less(i, j int) bool {
	iRate, jRate := s[i].getLossRate(), s[j].getLossRate()
	if iRate != jRate {
		return iRate < jRate
	}
	return s[i].Delay < s[j].Delay
}
func (s PingDelaySet) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}

// 下载速度排序
type DownloadSpeedSet []CloudflareIPData

func (s DownloadSpeedSet) Len() int {
	return len(s)
}
func (s DownloadSpeedSet) Less(i, j int) bool {
	return s[i].DownloadSpeed > s[j].DownloadSpeed
}
func (s DownloadSpeedSet) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}

func (s DownloadSpeedSet) Print() {
	if NoPrintResult() {
		return
	}
	if len(s) <= 0 { // IP数组长度(IP数量) 大于 0 时继续
		fmt.Println("\n[信息] 完整测速结果 IP 数量为 0，跳过输出结果。")
		return
	}
	dateString := convertToString(s) // 转为多维数组 [][]String
	if len(dateString) < PrintNum {  // 如果IP数组长度(IP数量) 小于  打印次数，则次数改为IP数量
		PrintNum = len(dateString)
	}
	headFormat := "%-16s%-5s%-5s%-5s%-6s%-11s%-5s\n"
	dataFormat := "%-18s%-8s%-8s%-8s%-10s%-15s%-8s\n"
	for i := 0; i < PrintNum; i++ {
		if len(dateString[i][0]) > 15 {
			headFormat = "%-40s%-5s%-5s%-5s%-6s%-11s%-5s\n"
			dataFormat = "%-42s%-8s%-8s%-8s%-10s%-15s%-8s\n"
			break
		}
	}
	fmt.Printf(headFormat, "IP 地址", "已发送", "已接收", "丢包率", "平均延迟", "下载速度 (MB/s)", "数据中心")
	for i := 0; i < PrintNum; i++ {
		fmt.Printf(dataFormat, dateString[i][0], dateString[i][1], dateString[i][2], dateString[i][3], 
			dateString[i][4], dateString[i][5], dateString[i][6])
	}
	if !noOutput() {
		fmt.Printf("\n完整测速结果已写入 %v 文件，可使用记事本/表格软件查看。\n", Output)
	}
}
