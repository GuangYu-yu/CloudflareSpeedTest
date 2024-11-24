package main

import (
	"bufio"
	"context"
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"math"
	"math/rand"
	"net"
	"net/http"
	"os"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/VividCortex/ewma"
	"golang.org/x/term"
)

// 全局变量定义
var (
	version, versionNew string
	
	// 配置参数
	Routines         = 200
	TCPPort          = 443
	PingTimes        = 4
	TestCount        = 10
	URL              = "https://cf.xiu2.xyz/url"
	Timeout          = 10 * time.Second
	Disable          = false
	MinSpeed float64 = 0.0
	
	// HTTP相关
	Httping           bool
	HttpingStatusCode int
	HttpingCFColo     string
	HttpingCFColomap  *sync.Map
	OutRegexp         = regexp.MustCompile(`[A-Z]{3}`)
	
	// IP相关
	TestAll4 = false
	IPFile   = "ip.txt"
	IPText   string
	More6    = false
	Lots6    = false
	Many6    = false
	Some6    = false
	Many4    = false
	V4Param  = ""
	V6Param  = ""
	
	// 输出相关
	InputMaxDelay    = 9999 * time.Millisecond
	InputMinDelay    = 0 * time.Millisecond
	InputMaxLossRate = float32(1.0)
	Output           = "result.csv"
	PrintNum         = 10
	ShowAirport      = false
	
	// 格式化字符串
	headFormat string
	dataFormat string
)

// 添加常量定义
var (
	// 基础表头列
	baseHeaders = []string{
		"IP 地址",
		"已发送",
		"已接收", 
		"丢包率",
		"平均延迟",
		"下载速度 (MB/s)",
	}
	// 机场码列
	airportHeader = "机场码"
)

// 进度条相关结构和方法
type Bar struct {
	current    int
	total      int
	prefix     string
	suffix     string
	finished   bool
	startTime  time.Time
	lastUpdate time.Time
	bandwidth  float64
	blocks     []string // 用于轮播的方块字符
	blockIndex int     // 当前方块索引
}

func NewBar(total int, prefix string, suffix string) *Bar {
	return &Bar{
		total:     total,
		prefix:    prefix,
		suffix:    suffix,
		startTime: time.Now(),
		blocks:    []string{"█", "▇", "▆", "▅", "▄", "▃", "▂", "▁"},
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
		
		termWidth := b.getTerminalWidth()
		
		var otherWidth int
		if b.bandwidth > 0 {
			otherWidth = len(fmt.Sprintf("%d / %d [] %.2f MB/s", b.current, b.total, b.bandwidth))
		} else {
			otherWidth = len(fmt.Sprintf("%d / %d [] %s", b.current, b.total, b.suffix))
		}
		
		barWidth := termWidth - otherWidth - 1
		if barWidth < 10 {
			barWidth = 10
		}
		
		percent := float64(b.current) / float64(b.total)
		filled := int(percent * float64(barWidth))
		if filled > barWidth {
			filled = barWidth
		}
		
		var bar string
		if filled < barWidth {
			bar = strings.Repeat("█", filled)
			
			b.blockIndex = (b.blockIndex + 1) % len(b.blocks)
			bar += b.blocks[b.blockIndex]
			
			bar += strings.Repeat("░", barWidth-filled-1)
		} else {
			bar = strings.Repeat("█", barWidth)
		}
		
		// 显示进度和网速
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

// IP数据结构
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
	Colo         string
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
	if ShowAirport {
		result := make([]string, 7)
		result[0] = cf.IP.String()
		result[1] = strconv.Itoa(cf.Sended)
		result[2] = strconv.Itoa(cf.Received)
		result[3] = strconv.FormatFloat(float64(cf.getLossRate()), 'f', 2, 32)
		result[4] = strconv.FormatFloat(cf.Delay.Seconds()*1000, 'f', 2, 32)
		result[5] = strconv.FormatFloat(cf.DownloadSpeed/1024/1024, 'f', 2, 32)
		result[6] = cf.Colo
		return result
	}
	
	// 不显示机场码时只返回6列
	result := make([]string, 6)
	result[0] = cf.IP.String()
	result[1] = strconv.Itoa(cf.Sended)
	result[2] = strconv.Itoa(cf.Received)
	result[3] = strconv.FormatFloat(float64(cf.getLossRate()), 'f', 2, 32)
	result[4] = strconv.FormatFloat(cf.Delay.Seconds()*1000, 'f', 2, 32)
	result[5] = strconv.FormatFloat(cf.DownloadSpeed/1024/1024, 'f', 2, 32)
	return result
}

// 延迟丢包排序
type PingDelaySet []CloudflareIPData

// 延迟条件过滤
func (s PingDelaySet) FilterDelay() (data PingDelaySet) {
	if InputMaxDelay > 9999*time.Millisecond || InputMinDelay < 0 {
		return s
	}
	if InputMaxDelay == 9999*time.Millisecond && InputMinDelay == 0 {
		return s
	}
	for _, v := range s {
		if v.Delay > InputMaxDelay {
			break
		}
		if v.Delay < InputMinDelay {
			continue
		}
		data = append(data, v)
	}
	return
}

// 丢包条件过滤
func (s PingDelaySet) FilterLossRate() (data PingDelaySet) {
	if InputMaxLossRate >= 1.0 {
		return s
	}
	for _, v := range s {
		if v.getLossRate() > InputMaxLossRate {
			break
		}
		data = append(data, v)
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
	
	// 如果延迟相差在 5ms 以内，进行随机排序
	if math.Abs(float64(s[i].Delay.Milliseconds() - s[j].Delay.Milliseconds())) <= 5 {
		// 使用随机数决定顺序
		return rand.Float64() < 0.5
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

// 是否打印测试结果
func NoPrintResult() bool {
	return PrintNum == 0
}

// 是否输出到文件
func noOutput() bool {
	return Output == "" || Output == " "
}

// 获取完整表头
func getHeaders() []string {
	if ShowAirport {
		return append(baseHeaders, airportHeader)
	}
	return baseHeaders
}

// 修改 ExportCsv 函数
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
	_ = w.Write(getHeaders())
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

// 通用的打印方法
func printResults(s interface{}, resultType string) {
	if NoPrintResult() {
		return
	}
	
	var data []CloudflareIPData
	switch v := s.(type) {
	case PingDelaySet:
		data = []CloudflareIPData(v)
	case DownloadSpeedSet:
		data = []CloudflareIPData(v)
	}
	
	if len(data) <= 0 {
		fmt.Printf("\n[信息] %s结果 IP 数量为 0，跳过输出结果。\n", resultType)
		return
	}
	
	dateString := convertToString(data)
	if len(dateString) < PrintNum {
		PrintNum = len(dateString)
	}
	
	if ShowAirport {
		headFormat = "%-16s%-5s%-5s%-5s%-6s%-15s  %-5s\n"
		dataFormat = "%-18s%-8s%-8s%-8s%-10s%-15.2f  %-5s\n"
	} else {
		headFormat = "%-16s%-5s%-5s%-5s%-6s%-11s\n"
		dataFormat = "%-18s%-8s%-8s%-8s%-10s%-15.2f\n"
	}
	
	for i := 0; i < PrintNum; i++ {
		if len(dateString[i][0]) > 15 {
			if ShowAirport {
				headFormat = "%-40s%-5s%-5s%-5s%-6s%-11s%-5s\n"
				dataFormat = "%-42s%-8s%-8s%-8s%-10s%-15s%-5s\n"
			} else {
				headFormat = "%-40s%-5s%-5s%-5s%-6s%-11s\n"
				dataFormat = "%-42s%-8s%-8s%-8s%-10s%-15s\n"
			}
			break
		}
	}
	
	headers := getHeaders()
	headerInterfaces := make([]interface{}, len(headers))
	for i, v := range headers {
		headerInterfaces[i] = v
	}
	fmt.Printf(headFormat, headerInterfaces...)
	
	for i := 0; i < PrintNum; i++ {
		fmt.Printf(dataFormat, 
			dateString[i][0],  // IP
			dateString[i][1],  // 已发送
			 dateString[i][2],  // 已接收
			 dateString[i][3],  // 丢包率
			 dateString[i][4],  // 平均延迟
			 dateString[i][5],  // 下载速度
		)
	}
	
	if !noOutput() {
		fmt.Printf("\n完整测速结果已写入 %v 文件，可使用记事本/表格软件查看。\n", Output)
	}
}

func (s PingDelaySet) Print() {
	printResults(s, "延迟测速")
}

func (s DownloadSpeedSet) Print() {
	printResults(s, "完整测速")
}

// 初始化函数
func init() {
	var printVersion bool
	var help = `
CloudflareSpeedTest ` + version + `
测试 Cloudflare CDN 所有 IP 的延迟和速度，获取最快 IP (IPv4+IPv6)！
https://github.com/XIU2/CloudflareSpeedTest

参数：
    -n 200
        延迟测速线程；越多延迟测速越快，性能弱的设备 (如路由器) 请勿太高；(默认 200 最多 1000)
    -t 4
        延迟测速次数；单个 IP 延迟测速的次数；(默认 4 次)
    -dn 10
        下载测速数量；延迟测速并排序后，从最低延迟起下载测速的数量；(默认 10 个)
    -dt 10
        下载测速时间；单个 IP 下载测速最长时间，不能太短；(默认 10 秒)
    -tp 443
        指定测速端口；延迟测速/下载测速时使用的端口；(默认 443 端口)
    -url https://cf.xiu2.xyz/url
        指定测速地址；延迟测速(HTTPing)/下载测速时使用的地址，默认地址不保证可用性，建议自建；

    -httping
        切换测速模式；延迟测速模式改为 HTTP 协议，所用测试地址为 [-url] 参数；(默认 TCPing)
    -httping-code 200
        有效状态代码；HTTPing 延迟测速时网页返回的有效 HTTP 状态码，仅限一个；(默认 200 301 302)
    -cfcolo HKG,KHH,NRT,LAX,SEA,SJC,FRA,MAD
        匹配指定地区；地区名为当地机场三字码，英文逗号分隔，仅 HTTPing 模式可用；(默认 所有地区)
    -aprt
        显示机场码；显示测速结果的机场码；(默认 不显示)

    -tl 200
        平均延迟上限；只输出低于指定平均延迟的 IP，各上下限条件可搭配使用；(默认 9999 ms)
    -tll 40
        平均延迟下限；只输出高于指定平均延迟的 IP；(默认 0 ms)
    -tlr 0.2
        丢包几率上限；只输出低于/等于指定丢包率的 IP，范围 0.00~1.00，0 过滤掉任何丢包的 IP；(默认 1.00)
    -sl 5
        下载速度下限；只输出高于指定下载速度的 IP，凑够指定数量 [-dn] 才会停止测速；(默认 0.00 MB/s)

    -p 10
        显示结果数量；测速后直接显示指定数量的结果，为 0 时不显示结果直接退出；(默认 10 个)
    -f ip.txt
        IP段数据文件；如路径含有空格请加上引号；支持其他 CDN IP段；(默认 ip.txt)
    -ip 1.1.1.1,2.2.2.2/24,2606:4700::/32
        指定IP段数据；直接通过参数指定要测速的 IP 段数据，英文逗号分隔；(默认 空)
    -o result.csv
        写入结果文件；如路径含有空格请加上引号；值为空时不写入文件 [-o ""]；(默认 result.csv)

    -dd
        禁用下载测速；禁用后测速结果会按延迟排序 (默认按下载速度排序)；(默认 启用)
    -all4
        测速全部的 IPv4；(IPv4 默认每 /24 段随机测速一个 IP)
    -more6
        测试更多 IPv6；(表示 -v6 18，即每个 CIDR 测速 2^18 即 262144 个)
    -lots6
        测试较多 IPv6；(表示 -v6 16，即每个 CIDR 测速 2^16 即 65536 个)
    -many6
        测试很多 IPv6；(表示 -v6 12，即每个 CIDR 测速 2^12 即 4096 个)
    -some6
        测试一些 IPv6；(表示 -v6 8，即每个 CIDR 测 2^8 即 256 个)
    -many4
        测试一点 IPv4；(表示 -v4 12，即每个 CIDR 测速 2^12 即 4096 个)

    -v4
        指定 IPv4 测试数量 (2^n±m，例如 -v4 0+12 表示 2^0+12 即每个 CIDR 测速 13 个)
    -v6
        指定 IPv6 测试数量 (2^n±m，例如 -v6 18-6 表示 2^18-6 即每个 CIDR 测速 262138 个)

    -v
        打印程序版本 + 检查版本更新
    -h
        打印帮助说明
`
	var minDelay, maxDelay, downloadTime int
	var maxLossRate float64
	flag.IntVar(&Routines, "n", 200, "延迟测速线程")
	flag.IntVar(&PingTimes, "t", 4, "延迟测速次数")
	flag.IntVar(&TestCount, "dn", 10, "下载测速数量")
	flag.IntVar(&downloadTime, "dt", 10, "下载测速时间")
	flag.IntVar(&TCPPort, "tp", 443, "指定测速端口")
	flag.StringVar(&URL, "url", "https://cf.xiu2.xyz/url", "指定测速地址")

	flag.BoolVar(&Httping, "httping", false, "切换测速模式")
	flag.IntVar(&HttpingStatusCode, "httping-code", 0, "有效状态代码")
	flag.StringVar(&HttpingCFColo, "cfcolo", "", "匹配指定地区")

	flag.IntVar(&maxDelay, "tl", 9999, "平均延迟上限")
	flag.IntVar(&minDelay, "tll", 0, "平均延迟下限")
	flag.Float64Var(&maxLossRate, "tlr", 1, "丢包几率上限")
	flag.Float64Var(&MinSpeed, "sl", 0, "下载速度下限")

	flag.IntVar(&PrintNum, "p", 10, "显示结果数量")
	flag.StringVar(&IPFile, "f", "ip.txt", "IP段数据文件")
	flag.StringVar(&IPText, "ip", "", "指定IP段数据")
	flag.StringVar(&Output, "o", "result.csv", "输出结果文件")

	flag.BoolVar(&Disable, "dd", false, "禁用下载测速")
	flag.BoolVar(&TestAll4, "all4", false, "测速全部 IPv4")

	flag.BoolVar(&More6, "more6", false, "测试更多 IPv6 (相当于 -v6 18)")
	flag.BoolVar(&Lots6, "lots6", false, "测试较多 IPv6 (相当于 -v6 16)")
	flag.BoolVar(&Many6, "many6", false, "测试很多 IPv6 (相当于 -v6 12)")
	flag.BoolVar(&Some6, "some6", false, "测试一些 IPv6 (相当于 -v6 8)")
	flag.BoolVar(&Many4, "many4", false, "测试很多 IPv4 (相当于 -v4 12)")

	flag.StringVar(&V4Param, "v4", "", "IPv4 测试数量 (2^n±m)")
	flag.StringVar(&V6Param, "v6", "", "IPv6 测试数量 (2^n±m)")

	flag.BoolVar(&printVersion, "v", false, "打印程序版本")
	flag.BoolVar(&ShowAirport, "aprt", false, "显示机场码")
	flag.Usage = func() { fmt.Print(help) }
	flag.Parse()

	if MinSpeed > 0 && time.Duration(maxDelay)*time.Millisecond == InputMaxDelay {
		fmt.Println("[小提示] 在使用 [-sl] 参数时，建议搭配 [-tl] 参数，以避免因凑不够 [-dn] 数量而一直测速...")
	}
	InputMaxDelay = time.Duration(maxDelay) * time.Millisecond
	InputMinDelay = time.Duration(minDelay) * time.Millisecond
	InputMaxLossRate = float32(maxLossRate)
	Timeout = time.Duration(downloadTime) * time.Second
	HttpingCFColomap = MapColoMap()

	if printVersion {
		println(version)
		fmt.Println("检查版本更新中...")
		checkUpdate()
		if versionNew != "" {
			fmt.Printf("*** 发现新版本 [%s]！请前往 [https://github.com/XIU2/CloudflareSpeedTest] 更新！ ***", versionNew)
		} else {
			fmt.Println("当前为最新版本 [" + version + "]！")
		}
		os.Exit(0)
	}
}

// 主函数
func main() {
	InitRandSeed() // 置随机数种子

	fmt.Printf("# XIU2/CloudflareSpeedTest %s \n\n", version)

	ping := NewPing()
	var allPingData PingDelaySet
	hasMore := true
	
	// 完成所有IP的延迟测试
	for hasMore {
		// 每次测试一批IP
		pingData, more := ping.RunBatch(Routines) // 使用 Routines 作为每批测试的数量
		hasMore = more
		
		// 过滤并添加到总结果中
		filteredData := pingData.FilterDelay().FilterLossRate()
		allPingData = append(allPingData, filteredData...)
	}

	// 按延迟排序
	sort.Sort(allPingData)
	
	// 如果禁用了下载测速
	if Disable {
		ExportCsv(DownloadSpeedSet(allPingData)) // 输出结果
		allPingData.Print()                      // 打印结果
	} else {
		// 取前 TestCount 个 IP 进行下载测速
		testCount := TestCount
		if testCount > len(allPingData) {
			testCount = len(allPingData)
		}
		speedData := TestDownloadSpeed(allPingData[:testCount])
		
		// 按下载速度排序
		sort.Sort(speedData)
		
		ExportCsv(speedData) // 输出结果
		speedData.Print()    // 打印结果
	}

	if versionNew != "" {
		fmt.Printf("\n*** 发现新版本 [%s]！请前往 [https://github.com/XIU2/CloudflareSpeedTest] 更新！ ***\n", versionNew)
	}
	endPrint()
}

func endPrint() {
	if NoPrintResult() {
		return
	}
	if runtime.GOOS == "windows" { // 如果是 Windows 系统，则需要按下 回车键 或 Ctrl+C 退出（避免通过双击运行时，测速完毕后直接关闭）
		fmt.Printf("按下 回车键 或 Ctrl+C 退出。")
		fmt.Scanln()
	}
}

// 检查更新
func checkUpdate() {
	timeout := 10 * time.Second
	client := http.Client{Timeout: timeout}
	res, err := client.Get("https://api.xiu2.xyz/ver/cloudflarespeedtest.txt")
	if err != nil {
		return
	}
	// 读取资源数据 body: []byte
	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return
	}
	// 关闭资源流
	defer res.Body.Close()
	if string(body) != version {
			versionNew = string(body)
	}
}

// Ping 结构体及相关方法
type Ping struct {
	wg       *sync.WaitGroup
	m        *sync.Mutex
	ips      []*net.IPAddr
	csv      PingDelaySet
	control  chan bool
	bar      *Bar
	position int
}

func NewPing() *Ping {
	checkPingDefault()
	ips := loadIPRanges()
	return &Ping{
		wg:       &sync.WaitGroup{},
		m:        &sync.Mutex{},
		ips:      ips,
		csv:      make(PingDelaySet, 0),
		control:  make(chan bool, Routines),
		bar:      NewBar(len(ips), "可用:", ""),
		position: 0,
	}
}

func checkPingDefault() {
	if Routines <= 0 {
		Routines = 200
	}
	if TCPPort <= 0 || TCPPort >= 65535 {
		TCPPort = 443
	}
	if PingTimes <= 0 {
		PingTimes = 4
	}
}

func (p *Ping) RunBatch(targetCount int) (PingDelaySet, bool) {
	// 只在第一次运行时初始化
	if p.position == 0 {
		mode := "TCP"
		if Httping {
			mode = "HTTP"
		}
		fmt.Printf("开始延迟测速（模式：%s, 端口：%d, 范围：%v ~ %v ms, 丢包：%.2f)\n",
			mode,
			TCPPort,
			InputMinDelay.Milliseconds(),
			InputMaxDelay.Milliseconds(),
			InputMaxLossRate,
		)

		// 初始化通道和结果集
		p.control = make(chan bool, Routines)
		p.csv = make(PingDelaySet, 0)

		// 初始化随机数种子
		rand.Seed(time.Now().UnixNano())
		
		// 一次性启动所有IP的测试
		for _, ip := range p.ips {
			p.wg.Add(1)
			p.control <- false
			go p.start(ip)
		}
		p.wg.Wait()

		// 排序结果
		sort.Sort(p.csv)
		p.position = len(p.ips)
	}

	return p.csv, false
}

func (p *Ping) start(ip *net.IPAddr) {
	defer p.wg.Done()
	p.tcpingHandler(ip)
	<-p.control
}

// HTTP测速相关
func (p *Ping) httping(ip *net.IPAddr) (int, time.Duration, string) {
	hc := http.Client{
		Timeout: time.Second * 2,
		Transport: &http.Transport{
			DialContext: getDialContext(ip),
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	var colo string
	// 先访问一次获取 HTTP 状态码和机场码
	{
		requ, err := http.NewRequest(http.MethodHead, URL, nil)
		if err != nil {
			return 0, 0, ""
		}
		requ.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_12_6) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/98.0.4758.80 Safari/537.36")
		resp, err := hc.Do(requ)
		if err != nil {
			return 0, 0, ""
		}
		defer resp.Body.Close()

		// 检查状态码
		if HttpingStatusCode == 0 || HttpingStatusCode < 100 && HttpingStatusCode > 599 {
			if resp.StatusCode != 200 && resp.StatusCode != 301 && resp.StatusCode != 302 {
				return 0, 0, ""
			}
		} else {
			if resp.StatusCode != HttpingStatusCode {
				return 0, 0, ""
			}
		}

		io.Copy(ioutil.Discard, resp.Body)

		// 获取机场码，不论是否指定了地区限制都获取
		cfRay := func() string {
			if resp.Header.Get("Server") == "cloudflare" {
				return resp.Header.Get("CF-RAY") // 示例 cf-ray: 7bd32409eda7b020-SJC
			}
			return resp.Header.Get("x-amz-cf-pop") // 示例 X-Amz-Cf-Pop: SIN52-P1
		}()
		
		// 提取三字码
		if cfRay != "" {
			colo = OutRegexp.FindString(cfRay)
			// 如果指定了地区限制，检查是否匹配
			if HttpingCFColo != "" {
				if _, ok := HttpingCFColomap.Load(colo); !ok {
					return 0, 0, ""
				}
			}
		}
	}

	// 循环测速计算延迟
	success := 0
	var delay time.Duration
	for i := 0; i < PingTimes; i++ {
		requ, err := http.NewRequest(http.MethodHead, URL, nil)
		if err != nil {
			log.Fatal("意外的错误，请报告：", err)
			return 0, 0, ""
		}
		requ.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_12_6) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/98.0.4758.80 Safari/537.36")
		if i == PingTimes-1 {
			requ.Header.Set("Connection", "close")
		}
		startTime := time.Now()
		resp, err := hc.Do(requ)
		if err != nil {
			continue
		}
		success++
		io.Copy(ioutil.Discard, resp.Body)
		_ = resp.Body.Close()
		duration := time.Since(startTime)
		delay += duration
	}

	return success, delay, colo
}

func MapColoMap() *sync.Map {
	if HttpingCFColo == "" {
		return nil
	}
	colos := strings.Split(strings.ToUpper(HttpingCFColo), ",")
	colomap := &sync.Map{}
	for _, colo := range colos {
		colomap.Store(colo, colo)
	}
	return colomap
}

func (p *Ping) getColo(b string) string {
	if b == "" {
		return ""
	}
	out := OutRegexp.FindString(b)

	if HttpingCFColomap == nil {
		return out
	}
	_, ok := HttpingCFColomap.Load(out)
	if ok {
		return out
	}
	return ""
}

// TCP测速相关
func (p *Ping) tcping(ip *net.IPAddr) (bool, time.Duration) {
	startTime := time.Now()
	var fullAddress string
	if isIPv4(ip.String()) {
		fullAddress = fmt.Sprintf("%s:%d", ip.String(), TCPPort)
	} else {
		fullAddress = fmt.Sprintf("[%s]:%d", ip.String(), TCPPort)
	}
	conn, err := net.DialTimeout("tcp", fullAddress, time.Second)
	if err != nil {
		return false, 0
	}
	defer conn.Close()
	duration := time.Since(startTime)
	return true, duration
}

func (p *Ping) checkConnection(ip *net.IPAddr) (recv int, totalDelay time.Duration, colo string) {
	if Httping {
		// HTTP 模式下总是获取机场码
		recv, totalDelay, colo = p.httping(ip)
		return
	}
	
	// TCP 模式：先进行 TCP 测试
	for i := 0; i < PingTimes; i++ {
		if ok, delay := p.tcping(ip); ok {
			recv++
			totalDelay += delay
		}
	}
	
	// 只有启用了 -aprt 参数时才获取机场码
	if ShowAirport && recv > 0 {
		_, _, colo = p.httping(ip)
	}
	
	return
}

func (p *Ping) tcpingHandler(ip *net.IPAddr) {
	recv, totalDelay, colo := p.checkConnection(ip)
	nowAble := len(p.csv)
	if recv != 0 {
		nowAble++
	}
	p.bar.Grow(1, strconv.Itoa(nowAble))
	if recv == 0 {
		return
	}
	
	data := &CloudflareIPData{
		PingData: &PingData{
			IP:       ip,
			Sended:   PingTimes,
			Received: recv,
			Delay:    totalDelay / time.Duration(recv),
		},
		Colo: colo,  // 使用从 checkConnection 返回的机场码
	}
	p.appendIPData(data)
}

func (p *Ping) appendIPData(data *CloudflareIPData) {
	p.m.Lock()
	defer p.m.Unlock()
	p.csv = append(p.csv, *data)
}

// IP 范围处理相关代码
func InitRandSeed() {
	rand.Seed(time.Now().UnixNano())
}

func isIPv4(ip string) bool {
	return strings.Contains(ip, ".")
}

func randIPEndWith(num byte) byte {
	if num == 0 {
		return byte(0)
	}
	return byte(rand.Intn(int(num)))
}

// 解析类似 "8-5" 或 "9" 这样的参数
func parseNumParam(param string) (int64, error) {
	if param == "" {
		return 0, nil
	}
	
	var base, offset int64
	var err error
	
	if idx := strings.IndexAny(param, "+-"); idx != -1 {
		base, err = strconv.ParseInt(param[:idx], 10, 64)
		if err != nil {
			return 0, err
		}
		offset, err = strconv.ParseInt(param[idx:], 10, 64)
		if err != nil {
			return 0, err
		}
		return int64(math.Pow(2, float64(base))) + offset, nil
	}
	
	base, err = strconv.ParseInt(param, 10, 64)
	if err != nil {
		return 0, err
	}
	return int64(math.Pow(2, float64(base))), nil
}

// 获取应该测试的 IP 数量
func getTestCount(isIPv4 bool) int64 {
	if isIPv4 {
		if TestAll4 {
			return math.MaxInt64
		}
		if Many4 {
			v4Count, _ := parseNumParam("12")
			if V4Param != "" {
				if count, err := parseNumParam(V4Param); err == nil && count < v4Count {
					return count
				}
			}
			return v4Count
		}
		if V4Param != "" {
			if count, err := parseNumParam(V4Param); err == nil {
				if count >= 0 && count <= int64(math.Pow(2, 16)) {
					return count
				}
			}
		}
		return 0
	} else {
		if More6 {
			v6Count, _ := parseNumParam("18")
			if V6Param != "" {
				if count, err := parseNumParam(V6Param); err == nil && count < v6Count {
					return count
				}
			}
			return v6Count
		}
		if Lots6 {
			v6Count, _ := parseNumParam("16")
			if V6Param != "" {
				if count, err := parseNumParam(V6Param); err == nil && count < v6Count {
					return count
				}
			}
			return v6Count
		}
		if Many6 {
			v6Count, _ := parseNumParam("12")
			if V6Param != "" {
				if count, err := parseNumParam(V6Param); err == nil && count < v6Count {
					return count
				}
			}
			return v6Count
		}
		if Some6 {
			v6Count, _ := parseNumParam("8")
			if V6Param != "" {
				if count, err := parseNumParam(V6Param); err == nil && count < v6Count {
					return count
				}
			}
			return v6Count
		}
		if V6Param != "" {
			if count, err := parseNumParam(V6Param); err == nil {
				if count >= 0 && count <= int64(math.Pow(2, 96)) {
					return count
				}
			}
		}
		return 0
	}
}

func (r *IPRanges) chooseIPv4() {
	if r.mask == "/32" {
		r.appendIP(r.firstIP)
		return
	}
	
	minIP, hosts := r.getIPRange()
	targetCount := getTestCount(true)
	
	if targetCount > 0 {
		if targetCount >= int64(hosts)+1 {
			for i := 0; i <= int(hosts); i++ {
				r.appendIPv4(byte(i) + minIP)
			}
			return
		}
		
		used := make(map[byte]bool)
		for int64(len(used)) < targetCount {
			ip := minIP + randIPEndWith(hosts)
			if !used[ip] {
				used[ip] = true
				r.appendIPv4(ip)
			}
		}
	} else {
		for r.ipNet.Contains(r.firstIP) {
			r.appendIPv4(minIP + randIPEndWith(hosts))
			r.firstIP[14]++
			if r.firstIP[14] == 0 {
				r.firstIP[13]++
				if r.firstIP[13] == 0 {
					r.firstIP[12]++
				}
			}
		}
	}
}

func (r *IPRanges) chooseIPv6() {
	if r.mask == "/128" {
		r.appendIP(r.firstIP)
		return
	}
	
	targetCount := getTestCount(false)
	
	if targetCount > 0 {
		used := make(map[string]bool)
		for int64(len(used)) < targetCount {
			newIP := make([]byte, len(r.firstIP))
			copy(newIP, r.firstIP)
			
			for i := len(newIP)-1; i >= 0; i-- {
				newIP[i] = byte(rand.Intn(256))
				if r.ipNet.Contains(newIP) {
					ipStr := net.IP(newIP).String()
					if !used[ipStr] {
						used[ipStr] = true
						r.appendIP(newIP)
						break
					}
				}
			}
		}
	} else {
		var tempIP uint8
		for r.ipNet.Contains(r.firstIP) {
			r.firstIP[15] = randIPEndWith(255)
			r.firstIP[14] = randIPEndWith(255)

			targetIP := make([]byte, len(r.firstIP))
			copy(targetIP, r.firstIP)
			r.appendIP(targetIP)

			for i := 13; i >= 0; i-- {
				tempIP = r.firstIP[i]
				r.firstIP[i] += randIPEndWith(255)
				if r.firstIP[i] >= tempIP {
					break
				}
			}
		}
	}
}

func loadIPRanges() []*net.IPAddr {
	ranges := newIPRanges()
	if IPText != "" {
		IPs := strings.Split(IPText, ",")
		for _, IP := range IPs {
			IP = strings.TrimSpace(IP)
			if IP == "" {
				continue
			}
			ranges.parseCIDR(IP)
			if isIPv4(IP) {
				ranges.chooseIPv4()
			} else {
				ranges.chooseIPv6()
			}
		}
	} else {
		if IPFile == "" {
			IPFile = "ip.txt"
		}
		file, err := os.Open(IPFile)
		if err != nil {
			log.Fatal(err)
		}
		defer file.Close()
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			ranges.parseCIDR(line)
			if isIPv4(line) {
				ranges.chooseIPv4()
			} else {
				ranges.chooseIPv6()
			}
		}
	}
	return ranges.ips
}

// 下载测速相关代码
func getDialContext(ip *net.IPAddr) func(ctx context.Context, network, address string) (net.Conn, error) {
	var fakeSourceAddr string
	if isIPv4(ip.String()) {
		fakeSourceAddr = fmt.Sprintf("%s:%d", ip.String(), TCPPort)
	} else {
		fakeSourceAddr = fmt.Sprintf("[%s]:%d", ip.String(), TCPPort)
	}
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, fakeSourceAddr)
	}
}

func TestDownloadSpeed(ipSet PingDelaySet) (speedSet DownloadSpeedSet) {
	if Disable {
		return DownloadSpeedSet(ipSet)
	}
	if len(ipSet) <= 0 {
		fmt.Println("\n[信息] 延迟测速结果 IP 数量为 0，跳过下载测速。")
		return
	}

	// 使用可用IP数量作为队列
	availableIPs := len(ipSet)
	testNum := TestCount
	if MinSpeed > 0 {
		testNum = availableIPs // 如果设置了速度下限,测试所有可用IP
	}
	
	fmt.Printf("开始下载测速（下限：%.2f MB/s, 数量：%d, 队列：%d）\n", MinSpeed, TestCount, availableIPs)
	bar := NewBar(TestCount, "", fmt.Sprintf("可用：%d", availableIPs))
	
	// 测试所有指定数量的IP
	for i := 0; i < testNum; i++ {
		speed := downloadHandler(ipSet[i].IP)
		ipSet[i].DownloadSpeed = speed
		// 用速度下限过滤
		if speed >= MinSpeed*1024*1024 {
			speedSet = append(speedSet, ipSet[i])
			bar.Grow(1, "")
			if len(speedSet) == TestCount { // 找到足够的IP后就停止
				break
			}
		}
	}
	
	bar.Done()
	
	if len(speedSet) == 0 { // 如果没有符合速度要求的,就返回所有结果
		speedSet = DownloadSpeedSet(ipSet[:testNum])
	}
	
	sort.Sort(speedSet) // 按速度排序
	return
}

func downloadHandler(ip *net.IPAddr) float64 {
	client := &http.Client{
			Transport: &http.Transport{DialContext: getDialContext(ip)},
			Timeout:   Timeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
					if len(via) > 10 {
							return http.ErrUseLastResponse
					}
					if req.Header.Get("Referer") == URL {
							req.Header.Del("Referer")
					}
					return nil
			},
	}
	req, err := http.NewRequest("GET", URL, nil)
	if err != nil {
		return 0.0
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_12_6) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/98.0.4758.80 Safari/537.36")

	response, err := client.Do(req)
	if err != nil {
		return 0.0
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return 0.0
	}

	timeStart := time.Now()
	timeEnd := timeStart.Add(Timeout)

	contentLength := response.ContentLength
	buffer := make([]byte, 1024)

	var contentRead int64 = 0
	timeSlice := Timeout / 100
	timeCounter := 1
	var lastContentRead int64 = 0

	var nextTime = timeStart.Add(timeSlice * time.Duration(timeCounter))
	e := ewma.NewMovingAverage()

	for contentLength != contentRead {
		currentTime := time.Now()
		if currentTime.After(nextTime) {
			timeCounter++
			nextTime = timeStart.Add(timeSlice * time.Duration(timeCounter))
			speed := float64(contentRead-lastContentRead) / timeSlice.Seconds()
			e.Add(speed)
			lastContentRead = contentRead
			
			// 不再在这里打印速度,避免干扰进度条显示
		}
		
		if currentTime.After(timeEnd) {
			break
		}
		bufferRead, err := response.Body.Read(buffer)
		if err != nil {
			if err != io.EOF {
				break
			} else if contentLength == -1 {
				break
			}
			last_time_slice := timeStart.Add(timeSlice * time.Duration(timeCounter-1))
			e.Add(float64(contentRead-lastContentRead) / (float64(currentTime.Sub(last_time_slice)) / float64(timeSlice)))
		}
		contentRead += int64(bufferRead)
	}
	
	return e.Value() / (Timeout.Seconds() / 120)
}

// IP范围相关结构体
type IPRanges struct {
    ips        []*net.IPAddr
    ipNet      *net.IPNet
    firstIP    net.IP
    mask       string
}

func newIPRanges() *IPRanges {
    return &IPRanges{
        ips: make([]*net.IPAddr, 0),
    }
}

func (r *IPRanges) parseCIDR(ip string) {
    // 尝试解析CIDR
    _, ipNet, err := net.ParseCIDR(ip)
    if err != nil {
        // 如果解析CIDR失败，尝试解析单个IP
        if ipAddr := net.ParseIP(ip); ipAddr != nil {
            ipNet = &net.IPNet{
                IP:   ipAddr,
                Mask: net.CIDRMask(32, 32),
            }
            if ipAddr.To4() == nil {
                ipNet.Mask = net.CIDRMask(128, 128)
            }
        } else {
            log.Fatal("解析IP失败:", ip)
        }
    }

    r.ipNet = ipNet
    r.firstIP = make([]byte, len(ipNet.IP))
    copy(r.firstIP, ipNet.IP)
    r.mask = net.IP(ipNet.Mask).String()
}

func (r *IPRanges) appendIP(ip net.IP) {
    ipAddr := &net.IPAddr{
        IP: make([]byte, len(ip)),
    }
    copy(ipAddr.IP, ip)
    r.ips = append(r.ips, ipAddr)
}

func (r *IPRanges) appendIPv4(lastByte byte) {
    ipAddr := &net.IPAddr{
        IP: make([]byte, 4),
    }
    copy(ipAddr.IP, r.firstIP.To4())
    ipAddr.IP[3] = lastByte
    r.ips = append(r.ips, ipAddr)
}

func (r *IPRanges) getIPRange() (byte, byte) {
    minIP := r.firstIP.To4()[3]
    maxIP := byte(0)
    switch r.mask {
    case "255.255.255.0":
        maxIP = 255
    case "255.255.0.0":
        maxIP = 255
    case "255.0.0.0":
        maxIP = 255
    default:
        maxIP = minIP
    }
    return minIP, maxIP - minIP
}
